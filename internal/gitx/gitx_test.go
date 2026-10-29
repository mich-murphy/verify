package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(ctx context.Context, t *testing.T, dir string, args ...string) {
	t.Helper()
	// #nosec G204 -- Test-owned temporary repositories and literal Git fixture argv.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixture(ctx context.Context, t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	git(ctx, t, dir, "init", "-b", "main")
	writeFile(t, filepath.Join(dir, "source.txt"), "base\n")
	git(ctx, t, dir, "add", ".")
	git(ctx, t, dir, "commit", "-m", "base")
	repo, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("Open returned nil repository without error")
	}
	return repo, dir
}

func head(ctx context.Context, t *testing.T, r *Repo) string {
	t.Helper()
	oid, err := r.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestRevisionRejectionIsValidationNotGitFailure(t *testing.T) {
	repo, _ := fixture(t.Context(), t)
	for _, ref := range []string{"", "--help", "HEAD\n", "HEAD\r", "HEAD\x00"} {
		got, err := repo.Resolve(t.Context(), ref)
		if got != "" || err == nil || err.Error() != "invalid revision" {
			t.Fatalf("revision %q: %q %v", ref, got, err)
		}
	}
}

func TestGitBufferExactBoundary(t *testing.T) {
	var b limitedBuffer
	const limit = 16 << 20
	for _, chunk := range []string{strings.Repeat("x", limit-1), "y", "z"} {
		n, err := b.Write([]byte(chunk))
		if n != len(chunk) || err != nil {
			t.Fatalf("write %d %v", n, err)
		}
		if b.buffer.Len() > limit {
			t.Fatal("unbounded buffer")
		}
		if b.overflow != (chunk == "z") {
			t.Fatalf("incorrect boundary overflow after %q", chunk[len(chunk)-1:])
		}
	}
	if b.buffer.Len() != limit || !strings.HasSuffix(b.String(), "xy") {
		t.Fatal("prefix not retained")
	}
}

func TestCommittedDirectoryAndMissingFileRejection(t *testing.T) {
	repo, dir := fixture(t.Context(), t)
	writeFile(t, filepath.Join(dir, "nested-file"), "file")
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "nested", "child"), "child")
	git(t.Context(), t, dir, "add", ".")
	git(t.Context(), t, dir, "commit", "-m", "nested")
	for _, path := range []string{"nested", "absent"} {
		if _, err := repo.ShowFile(t.Context(), "HEAD", path); err == nil || err.Error() != "path is not a regular committed file" {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestFullBranchMergeBaseAndShowFile(t *testing.T) {
	ctx := context.Background()
	repo, dir := fixture(ctx, t)
	base := head(ctx, t, repo)
	git(ctx, t, dir, "checkout", "-b", "feature")
	writeFile(t, filepath.Join(dir, "shared.go"), "important early change\n")
	git(ctx, t, dir, "add", ".")
	git(ctx, t, dir, "commit", "-m", "shared")
	writeFile(t, filepath.Join(dir, "notes.md"), "last docs only\n")
	git(ctx, t, dir, "add", ".")
	git(ctx, t, dir, "commit", "-m", "docs")
	candidate := head(ctx, t, repo)
	git(ctx, t, dir, "checkout", "main")
	writeFile(t, filepath.Join(dir, "target.txt"), "target advanced\n")
	git(ctx, t, dir, "add", ".")
	git(ctx, t, dir, "commit", "-m", "target")
	got, err := repo.MergeBase(ctx, "main", candidate)
	if err != nil || got != base {
		t.Fatalf("merge-base %s %v want %s", got, err, base)
	}
	data, err := repo.ShowFile(ctx, candidate, "shared.go")
	if err != nil || string(data) != "important early change\n" {
		t.Fatalf("committed show %q %v", data, err)
	}
	if _, err = repo.ShowFile(ctx, candidate, "../source.txt"); err == nil {
		t.Fatal("accepted path escape")
	}
	if _, err = repo.Resolve(ctx, "--help"); err == nil {
		t.Fatal("accepted option revision")
	}
}

func FuzzRepositoryPath(f *testing.F) {
	for _, seed := range []string{"gate.yaml", "../escape", "a/b", "/absolute", "a/../b", "a\x00b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if validPath(path) {
			if filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, "../") {
				t.Fatalf("unsafe accepted path %q", path)
			}
		}
	})
}

func TestHooksNotRunAndInheritedGitEnvIgnored(t *testing.T) {
	ctx := context.Background()
	repo, dir := fixture(ctx, t)
	oid := head(ctx, t, repo)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", "/bad")
	if head(ctx, t, repo) != oid {
		t.Fatal("inherited Git redirection")
	}
	hooks := t.TempDir()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	// #nosec G306 -- Executable fixture hook must be executable to prove hook suppression.
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	git(ctx, t, dir, "config", "core.hooksPath", hooks)
	wt := filepath.Join(t.TempDir(), "wt")
	if err := repo.AddWorktree(ctx, wt, oid); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("checkout hook executed", err)
	}
}

func TestShowFileRejectsSymlinkAndOversize(t *testing.T) {
	ctx := context.Background()
	repo, dir := fixture(ctx, t)
	if err := os.Symlink("source.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "large.txt"), strings.Repeat("a", 17<<20))
	git(ctx, t, dir, "add", ".")
	git(ctx, t, dir, "commit", "-m", "link and large")
	if _, err := repo.ShowFile(ctx, "HEAD", "link"); err == nil {
		t.Fatal("symlink treated as policy file")
	}
	if _, err := repo.ShowFile(ctx, "HEAD", "large.txt"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatal("output cap bypassed", err)
	}
}

func TestReplacementObjectsIgnored(t *testing.T) {
	ctx := t.Context()
	repo, dir := fixture(ctx, t)
	candidate := head(ctx, t, repo)
	writeFile(t, filepath.Join(dir, "source.txt"), "alternate\n")
	git(ctx, t, dir, "commit", "-am", "alternate")
	git(ctx, t, dir, "replace", candidate, head(ctx, t, repo))
	t.Setenv("GIT_NO_REPLACE_OBJECTS", "0")
	got, err := repo.ShowFile(ctx, candidate, "source.txt")
	if err != nil || string(got) != "base\n" {
		t.Fatalf("replacement tree read: %q %v", got, err)
	}
}
