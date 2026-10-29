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

func TestWorktreeMarkerReleaseCannotRemoveSameInodeReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "check")
	release, err := BeginWorktreeUse(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := WorktreeQuarantine(path)
	before, err := os.Lstat(marker)
	if err != nil {
		t.Fatal(err)
	}
	// WriteFile truncates and rewrites the existing inode, deterministically
	// exposing the weakness of checking only os.SameFile.
	replacement := []byte("another owner\n")
	if err := os.WriteFile(marker, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(marker)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("replacement did not retain inode: %v", err)
	}
	if err := release(); err == nil {
		t.Fatal("removed same-inode replacement marker")
	}
	// #nosec G304 -- Marker is confined to this test temporary directory.
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != string(replacement) {
		t.Fatalf("replaced marker lost: %q %v", data, err)
	}
}

func TestWorktreeMarkerOwnershipTokensAndRepeatRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "check")
	first, err := BeginWorktreeUse(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := WorktreeQuarantine(path)
	// #nosec G304 -- Marker is confined to this test temporary directory.
	firstContent, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := first(); err != nil {
		t.Fatal(err)
	}
	second, err := BeginWorktreeUse(path)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- Marker is confined to this test temporary directory.
	secondContent, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstContent) == string(secondContent) {
		t.Fatal("independent acquisitions reused ownership token")
	}
	if err := first(); err == nil {
		t.Fatal("repeat release removed later owner's marker")
	}
	// #nosec G304 -- Marker is confined to this test temporary directory.
	remaining, err := os.ReadFile(marker)
	if err != nil || string(remaining) != string(secondContent) {
		t.Fatalf("later owner's marker lost: %q %v", remaining, err)
	}
	if err := second(); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeMarkerReleaseCannotRemoveSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "check")
	release, err := BeginWorktreeUse(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := WorktreeQuarantine(path)
	// #nosec G304 -- Marker is confined to this test temporary directory.
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(path), "target")
	// #nosec G703 -- Target is confined to this test temporary directory.
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, marker); err != nil {
		t.Fatal(err)
	}
	if err := release(); err == nil {
		t.Fatal("removed symlink replacement marker")
	}
	if info, err := os.Lstat(marker); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replacement lost: %v", err)
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
