package gitx

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Exercise Git's actual quoting, not a hand-written approximation of it.
func TestBattleQuotedPathDiffs(t *testing.T) {
	for _, name := range []string{"tab\t", "quote\".txt", "café.txt", "space name.txt", "line\nname.txt", "back\\slash.txt"} {
		t.Run(name, func(t *testing.T) {
			r, dir := fixture(t.Context(), t)
			git(t.Context(), t, dir, "config", "core.quotePath", "false")
			p := filepath.Join(dir, name)
			writeFile(t, p, "before\n")
			git(t.Context(), t, dir, "add", "--", name)
			git(t.Context(), t, dir, "commit", "-m", "path base")
			base := head(t.Context(), t, r)
			writeFile(t, p, "after\n")
			git(t.Context(), t, dir, "commit", "-am", "edit")
			d, err := r.DiffLines(t.Context(), base, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(d.Modified, name) || !containsLine(d.Added, Line{Path: name, Number: 1, Text: "after"}) {
				t.Errorf("text diff lost exact path %q: %+v", name, d)
			}
			base = head(t.Context(), t, r)
			if err := os.Chmod(p, 0o700); err != nil { // #nosec G302 -- Executable bit is the mode-only diff test subject.
				t.Fatal(err)
			}
			git(t.Context(), t, dir, "add", "--", name)
			git(t.Context(), t, dir, "commit", "-m", "mode only")
			d, err = r.DiffLines(t.Context(), base, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(d.Modified, []string{name}) {
				t.Errorf("mode diff lost exact path %q: %+v", name, d)
			}
		})
	}
}

func TestBattleBinaryQuotedTestDeletion(t *testing.T) {
	r, dir := fixture(t.Context(), t)
	const name = "café_test.go"
	writeFile(t, filepath.Join(dir, name), "// binary fixture\x00\n")
	git(t.Context(), t, dir, "add", "--", name)
	git(t.Context(), t, dir, "commit", "-m", "binary test fixture")
	base := head(t.Context(), t, r)
	git(t.Context(), t, dir, "rm", "--", name)
	git(t.Context(), t, dir, "commit", "-m", "delete")
	d, err := r.DiffLines(t.Context(), base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.DeletedFiles, []string{name}) {
		t.Fatalf("binary deleted test disappears from guard input: %+v", d)
	}
}

func TestBattleOpenRootTrailingSpace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo ")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t.Context(), t, dir, "init", "-b", "main")
	r, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root != real {
		t.Fatalf("root %q, want %q", r.Root, real)
	}
}
