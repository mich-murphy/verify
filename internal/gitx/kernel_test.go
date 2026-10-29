package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func kgit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper with fixed executable
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestKernelHelpers(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kgit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kgit(t, dir, "add", ".")
	kgit(t, dir, "commit", "-qm", "base")
	kgit(t, dir, "checkout", "-qb", "feat")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n3\n4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kgit(t, dir, "commit", "-qam", "change")
	ctx := t.Context()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.Changes(ctx, "main", "feat")
	if err != nil || len(cs) != 1 || cs[0].Path != "a.txt" || cs[0].Added != 2 || cs[0].Deleted != 1 {
		t.Fatalf("%+v %v", cs, err)
	}
	if b, err := r.Branch(ctx); err != nil || b != "feat" {
		t.Fatal(b, err)
	}
	if _, found, err := r.FileAt(ctx, "main", "nope"); err != nil || found {
		t.Fatal(found, err)
	}
	if d, found, err := r.FileAt(ctx, "main", "a.txt"); err != nil || !found || string(d) != "1\n2\n" {
		t.Fatal(string(d), found, err)
	}
	if _, _, err := r.FileAt(ctx, "main", "../x"); err == nil {
		t.Fatal("want invalid path error")
	}
	wt := filepath.Join(r.CommonDir, "gate", "worktrees", "w")
	if err := r.AddWorktree(ctx, wt, "feat"); err != nil {
		t.Fatal(err)
	}
	if d, _ := os.ReadFile(filepath.Join(wt, "a.txt")); !strings.HasPrefix(string(d), "1\n3") { // #nosec G304 -- test path
		t.Fatal(string(d))
	}
	if err := r.RemoveWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("worktree remains")
	}
}

func TestResetWorktree(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kgit(t, dir, "init", "-q", "-b", "main")
	write := func(p, s string) {
		t.Helper()
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "one\n")
	write(".gitignore", "ignored.txt\n")
	kgit(t, dir, "add", ".")
	kgit(t, dir, "commit", "-qm", "c1")
	write("a.txt", "two\n")
	write("b.txt", "b\n")
	kgit(t, dir, "add", ".")
	kgit(t, dir, "commit", "-qm", "c2")
	ctx := t.Context()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := "HEAD~1", "HEAD"
	wt := filepath.Join(r.CommonDir, "gate", "worktrees", "check")
	keep := []string{"node_modules", ".turbo"}
	read := func(p string) string {
		d, _ := os.ReadFile(filepath.Join(wt, p)) // #nosec G304 -- test path
		return string(d)
	}
	exists := func(p string) bool { _, err := os.Lstat(filepath.Join(wt, p)); return err == nil }

	if err := r.ResetWorktree(ctx, wt, c1, keep); err != nil || read("a.txt") != "one\n" || exists("b.txt") {
		t.Fatalf("create: %v %q", err, read("a.txt"))
	}
	for _, p := range []string{"stray.txt", "ignored.txt", "node_modules/x/y.js", ".turbo/c", "sub/z.txt", "other_modules/q"} {
		full := filepath.Join(wt, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.ResetWorktree(ctx, wt, c2, keep); err != nil {
		t.Fatal(err)
	}
	if read("a.txt") != "two\n" || !exists("b.txt") {
		t.Fatal("did not move to new sha")
	}
	for _, p := range []string{"stray.txt", "ignored.txt", "sub", "other_modules"} {
		if exists(p) {
			t.Errorf("%s survived reset", p)
		}
	}
	for _, p := range []string{"node_modules/x/y.js", ".turbo/c"} {
		if !exists(p) {
			t.Errorf("%s was not kept", p)
		}
	}

	corrupt := map[string]func(){
		"plain dir": func() {
			must(t, os.RemoveAll(wt))
			must(t, os.MkdirAll(wt, 0o750))
			must(t, os.WriteFile(filepath.Join(wt, "junk"), []byte("x"), 0o600))
		},
		"dangling .git": func() {
			must(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /nonexistent/x\n"), 0o600))
		},
		"foreign repo": func() {
			must(t, os.RemoveAll(wt))
			must(t, os.MkdirAll(wt, 0o750))
			kgit(t, wt, "init", "-q")
		},
	}
	for name, f := range corrupt {
		f()
		if err := r.ResetWorktree(ctx, wt, c1, keep); err != nil || read("a.txt") != "one\n" || exists("junk") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := r.ResetWorktree(ctx, wt, "nope", keep); err == nil {
		t.Fatal("want error for bad sha")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
