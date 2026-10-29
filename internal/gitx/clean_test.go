package gitx

import (
	"context"
	"path/filepath"
	"testing"
)

func TestIgnoredArtifactsAreNotDirtySource(t *testing.T) {
	ctx := context.Background()
	repo, dir := fixture(ctx, t)
	writeFile(t, filepath.Join(dir, ".gitignore"), "build.out\n")
	git(ctx, t, dir, "add", ".gitignore")
	git(ctx, t, dir, "commit", "-m", "ignore fixture output")
	writeFile(t, filepath.Join(dir, "build.out"), "ignored cache")
	if clean, err := repo.Clean(ctx); err != nil || !clean {
		t.Fatalf("ignored artifact counted as dirty source: %v, %v", clean, err)
	}
	git(ctx, t, dir, "add", "-f", "build.out")
	if clean, err := repo.Clean(ctx); err != nil || clean {
		t.Fatalf("staged ignored file hidden: %v, %v", clean, err)
	}
	git(ctx, t, dir, "commit", "-m", "track previously ignored file")
	writeFile(t, filepath.Join(dir, "build.out"), "changed tracked input")
	if clean, err := repo.Clean(ctx); err != nil || clean {
		t.Fatalf("tracked ignored file mutation hidden: %v, %v", clean, err)
	}
}
