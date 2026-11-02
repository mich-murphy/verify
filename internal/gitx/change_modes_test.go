package gitx

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/runner"
)

func TestChangesExactRevisionModes(t *testing.T) {
	for _, name := range []string{"plain.md", "tab\tname.md", "line\nname.md", "quote\"name.md", "-option.md"} {
		t.Run(name, func(t *testing.T) {
			r, dir := fixture(t.Context(), t)
			git(t.Context(), t, dir, "config", "core.filemode", "true")
			p := filepath.Join(dir, name)
			writeFile(t, p, "prose\n")
			git(t.Context(), t, dir, "add", "--", name)
			git(t.Context(), t, dir, "commit", "-m", "plain")
			base := head(t.Context(), t, r)
			if err := os.Chmod(p, 0o700); err != nil { // #nosec G302 -- Executable mode is the subject of this regression test.
				t.Fatal(err)
			}
			git(t.Context(), t, dir, "add", "--", name)
			git(t.Context(), t, dir, "commit", "-m", "mode only")
			executable := head(t.Context(), t, r)
			assertChanges(t, r, base, executable, []Change{{Path: name, OldMode: "100644", NewMode: "100755"}})
			// Inspect an older exact revision while the checkout has the opposite mode.
			if err := os.Chmod(p, 0o600); err != nil {
				t.Fatal(err)
			}
			assertChanges(t, r, base, executable, []Change{{Path: name, OldMode: "100644", NewMode: "100755"}})
			git(t.Context(), t, dir, "add", "--", name)
			git(t.Context(), t, dir, "commit", "-m", "remove executable bit")
			assertChanges(t, r, executable, "HEAD", []Change{{Path: name, OldMode: "100755", NewMode: "100644"}})
			git(t.Context(), t, dir, "rm", "--", name)
			git(t.Context(), t, dir, "commit", "-m", "delete")
			assertChanges(t, r, executable, "HEAD", []Change{{Path: name, Deleted: 1, OldMode: "100755", NewMode: "000000"}})
		})
	}
}

func TestChangesRenameRetainsCriticalOldPath(t *testing.T) {
	r, dir := fixture(t.Context(), t)
	writeFile(t, filepath.Join(dir, ".prekignore"), "ignored\n")
	git(t.Context(), t, dir, "add", ".")
	git(t.Context(), t, dir, "commit", "-m", "discovery control")
	base := head(t.Context(), t, r)
	git(t.Context(), t, dir, "mv", ".prekignore", "guide.md")
	git(t.Context(), t, dir, "commit", "-m", "rename")
	assertChanges(t, r, base, "HEAD", []Change{
		{Path: ".prekignore", Deleted: 1, OldMode: "100644", NewMode: "000000"},
		{Path: "guide.md", Added: 1, OldMode: "000000", NewMode: "100644"},
	})
}

func TestChangesBinaryAndType(t *testing.T) {
	r, dir := fixture(t.Context(), t)
	writeFile(t, filepath.Join(dir, "binary\t\n.png"), "before\x00data")
	writeFile(t, filepath.Join(dir, "link.md"), "regular\n")
	git(t.Context(), t, dir, "add", ".")
	git(t.Context(), t, dir, "commit", "-m", "base files")
	base := head(t.Context(), t, r)
	writeFile(t, filepath.Join(dir, "binary\t\n.png"), "after\x00data")
	if err := os.Remove(filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	git(t.Context(), t, dir, "add", ".")
	git(t.Context(), t, dir, "commit", "-m", "binary and type")
	assertChanges(t, r, base, "HEAD", []Change{
		{Path: "binary\t\n.png", Binary: true, OldMode: "100644", NewMode: "100644"},
		{Path: "link.md", Added: 1, Deleted: 1, OldMode: "100644", NewMode: "120000"},
	})
}

func assertChanges(t *testing.T, r *Repo, base, sha string, want []Change) {
	t.Helper()
	got, err := r.Changes(t.Context(), base, sha)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("changes=%+v want=%+v err=%v", got, want, err)
	}
}

func TestChangesBoundedSingleDiff(t *testing.T) {
	oid := strings.Repeat("a", 40)
	diff := ":100644 100755 " + oid + " " + oid + " M\x00docs/prose.md\x000\t0\tdocs/prose.md\x00"
	for _, truncated := range []bool{false, true} {
		r := &Repo{Root: t.TempDir(), git: "git"}
		commands := &battleGitCommands{run: func(call int, cmd runner.Command) runner.Result {
			out := oid + "\n"
			if call == 3 {
				args := []string{"diff", "--raw", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", oid, oid, "--"}
				if !slices.Equal(cmd.Args[len(cmd.Args)-len(args):], args) {
					t.Fatalf("unexpected diff argv: %v", cmd.Args)
				}
				out = diff
			}
			if call > 3 {
				t.Fatalf("per-file or redundant Git invocation: %v", cmd.Args)
			}
			return runner.Result{Status: runner.Pass, Settled: true, Stdout: runner.Evidence{Output: out, Truncated: call == 3 && truncated}}
		}}
		r.commands = commands
		got, err := r.Changes(t.Context(), "base", "candidate")
		if (err != nil) != truncated || commands.calls != 3 || truncated && got != nil {
			t.Fatalf("changes=%+v err=%v calls=%d", got, err, commands.calls)
		}
	}
}

func TestParseChangeModesRejectsIncompleteEvidence(t *testing.T) {
	raw := ":100644 100755 a b M\x00x\x00"
	for _, out := range []string{
		raw, raw + "0\t0\tx", raw + raw + "0\t0\tx\x00", raw + "0\t0\ty\x00", raw + "x\t0\tx\x00",
		raw + "-\t0\tx\x00", raw + "-1\t0\tx\x00", raw + "0\t0\tx\x000\t0\tx\x00",
		strings.Replace(raw, "100755", "999999", 1) + "0\t0\tx\x00",
		":100644\x00x\x000\t0\tx\x00",
	} {
		if got, err := parseChangeModes([]byte(out)); err == nil {
			t.Fatalf("accepted %q: %+v", out, got)
		}
	}
}
