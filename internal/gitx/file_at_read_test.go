package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/runner"
)

func TestFileAtValidatedDirectRead(t *testing.T) {
	oid := strings.Repeat("a", 40)
	path := "directory/file\tname.txt"
	metadata := "100644 blob " + strings.Repeat("b", 40) + "\t" + path + "\x00"
	for _, tc := range []struct {
		name      string
		metadata  string
		failAt    int
		truncated bool
		wantFound bool
		wantErr   bool
		wantCalls int
	}{
		{name: "regular", metadata: metadata, wantFound: true, wantCalls: 3},
		{name: "absent", wantCalls: 2},
		{name: "nonregular", metadata: strings.Replace(metadata, "100644", "120000", 1), wantErr: true, wantCalls: 2},
		{name: "wrong path", metadata: strings.Replace(metadata, path, "other", 1), wantErr: true, wantCalls: 2},
		{name: "malformed", metadata: "bad metadata", wantErr: true, wantCalls: 2},
		{name: "resolve error", failAt: 1, wantErr: true, wantCalls: 1},
		{name: "metadata error", failAt: 2, wantErr: true, wantCalls: 2},
		{name: "read error", metadata: metadata, failAt: 3, wantErr: true, wantCalls: 3},
		{name: "read overflow", metadata: metadata, truncated: true, wantErr: true, wantCalls: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Repo{Root: t.TempDir(), git: "git"}
			cause := errors.New("injected Git failure")
			commands := &battleGitCommands{run: func(call int, cmd runner.Command) runner.Result {
				var args []string
				out := ""
				switch call {
				case 1:
					args = []string{"rev-parse", "--verify", "--end-of-options", "branch^{commit}"}
					out = oid + "\n"
				case 2:
					args = []string{"ls-tree", "-z", oid, "--", path}
					out = tc.metadata
				case 3:
					args = []string{"cat-file", "blob", oid + ":" + path}
					out = "exact\x00contents\n"
				default:
					t.Fatalf("redundant Git command: %v", cmd.Args)
				}
				if !slices.Equal(cmd.Args[len(cmd.Args)-len(args):], args) {
					t.Fatalf("wrong resolved read argv: %v, want suffix %v", cmd.Args, args)
				}
				result := runner.Result{Status: runner.Pass, Settled: true, Stdout: runner.Evidence{Output: out}}
				if call == tc.failAt {
					result.Status, result.ProcessError = runner.Fail, cause
				}
				result.Stdout.Truncated = call == 3 && tc.truncated
				return result
			}}
			r.commands = commands
			data, found, err := r.FileAt(t.Context(), "branch", path)
			if found != tc.wantFound || (err != nil) != tc.wantErr || commands.calls != tc.wantCalls {
				t.Fatalf("data=%q found=%v err=%v calls=%d", data, found, err, commands.calls)
			}
			if found && string(data) != "exact\x00contents\n" || !found && len(data) != 0 {
				t.Fatalf("unexpected contents: %q", data)
			}
			if tc.failAt != 0 && !errors.Is(err, cause) {
				t.Fatalf("lost command error: %v", err)
			}
			if tc.name == "nonregular" && !errors.Is(err, ErrNonRegularFile) {
				t.Fatalf("lost nonregular sentinel: %v", err)
			}
		})
	}
}

func TestFileAtDirectReadRealGit(t *testing.T) {
	r, dir := fixture(t.Context(), t)
	for _, path := range []string{"space name.txt", "tab\tname.txt", "-option.txt", "café.txt"} {
		writeFile(t, filepath.Join(dir, path), "committed\x00content\n")
	}
	writeFile(t, filepath.Join(dir, "large.txt"), strings.Repeat("a", (16<<20)+1))
	if err := os.Symlink("source.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	kgit(t, dir, "add", ".")
	kgit(t, dir, "commit", "-qm", "read fixture")
	oid := head(t.Context(), t, r)
	for _, path := range []string{"space name.txt", "tab\tname.txt", "-option.txt", "café.txt"} {
		writeFile(t, filepath.Join(dir, path), "uncommitted content")
		data, found, err := r.FileAt(t.Context(), oid, path)
		if err != nil || !found || string(data) != "committed\x00content\n" {
			t.Fatalf("%q: data=%q found=%v err=%v", path, data, found, err)
		}
	}
	for _, path := range []string{"../source.txt", "line\nname", "large.txt", "link"} {
		if data, found, err := r.FileAt(t.Context(), oid, path); err == nil || found || len(data) != 0 {
			t.Fatalf("unsafe read %q: bytes=%d found=%v err=%v", path, len(data), found, err)
		}
	}
	if _, found, err := r.FileAt(t.Context(), oid, "absent"); err != nil || found {
		t.Fatalf("absent: found=%v err=%v", found, err)
	}
}
