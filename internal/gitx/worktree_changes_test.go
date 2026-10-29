package gitx

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWorktreeChanges(t *testing.T) {
	ctx := context.Background()
	repo, dir := fixture(ctx, t)
	writeFile(t, filepath.Join(dir, ".gitignore"), "ignored.out\n")
	writeFile(t, filepath.Join(dir, "staged.txt"), "s\n")
	writeFile(t, filepath.Join(dir, "gone.txt"), "g\n")
	writeFile(t, filepath.Join(dir, "with space.txt"), "w\n")
	git(ctx, t, dir, "add", ".")
	git(ctx, t, dir, "commit", "-m", "more")

	if got, err := repo.WorktreeChanges(ctx); err != nil || len(got) != 0 {
		t.Fatalf("clean tree: %v %v", got, err)
	}
	writeFile(t, filepath.Join(dir, "source.txt"), "unstaged\n")
	writeFile(t, filepath.Join(dir, "staged.txt"), "changed\n")
	git(ctx, t, dir, "add", "staged.txt")
	writeFile(t, filepath.Join(dir, "with space.txt"), "changed\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "new file.txt"), "n\n")
	writeFile(t, filepath.Join(dir, "ignored.out"), "i\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sub", "u.go"), "package sub\n")

	got, err := repo.WorktreeChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"new file.txt", "source.txt", "staged.txt", "sub/u.go", "with space.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}
