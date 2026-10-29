package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrackedIdentityAndDurableQuarantine(t *testing.T) {
	r, _ := fixture(t.Context(), t)
	sha := head(t.Context(), t, r)
	dir := filepath.Join(r.CommonDir, "gate", "worktrees", "identity")
	if err := r.ResetWorktree(t.Context(), dir, sha, nil); err != nil {
		t.Fatal(err)
	}
	assert := func(want bool) {
		t.Helper()
		err := r.AssertWorktreeIdentity(t.Context(), dir, sha)
		if (err == nil) != want {
			t.Fatalf("identity good=%t error=%v", want, err)
		}
	}
	assert(true)
	if err := os.WriteFile(filepath.Join(dir, "output"), []byte("build artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	assert(true)
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("uncommitted fix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assert(false)
	if err := r.ResetWorktree(t.Context(), dir, sha, nil); err != nil {
		t.Fatal(err)
	}
	assert(true)
	git(t.Context(), t, dir, "add", "source.txt")
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Stage a differing index entry while the worktree itself matches HEAD.
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("index changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t.Context(), t, dir, "add", "source.txt")
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assert(false)
	release, err := BeginWorktreeUse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckWorktreeQuarantine(dir); err == nil {
		t.Fatal("in-progress marker lost across runner instances")
	}
	if _, err := BeginWorktreeUse(dir); !os.IsExist(err) {
		t.Fatalf("adopted another owner's marker: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := CheckWorktreeQuarantine(dir); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeMarkerReleaseCannotRemoveReplacement(t *testing.T) {
	dir := t.TempDir()
	release, err := BeginWorktreeUse(filepath.Join(dir, "check"))
	if err != nil {
		t.Fatal(err)
	}
	marker := WorktreeQuarantine(filepath.Join(dir, "check"))
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("another owner\n")
	if err = os.WriteFile(marker, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = release(); err == nil {
		t.Fatal("removed replacement marker")
	}
	// #nosec G304 -- Marker is confined to this test temporary directory.
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != string(replacement) {
		t.Fatalf("replaced marker lost: %q %v", data, err)
	}
}
