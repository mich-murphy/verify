package gitx

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAddedHunksPerCommit(t *testing.T) {
	ctx := context.Background()
	repo, dir := fixture(ctx, t)
	base := head(ctx, t, repo)
	writeFile(t, filepath.Join(dir, "source.txt"), "base\nA\nB\n")
	writeFile(t, filepath.Join(dir, "bin.dat"), "a\x00b")
	git(ctx, t, dir, "add", "-A")
	git(ctx, t, dir, "commit", "-m", "one")
	c1 := head(ctx, t, repo)
	writeFile(t, filepath.Join(dir, "source.txt"), "X\nbase\nA\nB\nC\n")
	git(ctx, t, dir, "add", "-A")
	git(ctx, t, dir, "commit", "-m", "two")
	c2 := head(ctx, t, repo)

	var got []AddedHunk
	err := repo.AddedHunks(ctx, base, c2, func(h AddedHunk) error { got = append(got, h); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []AddedHunk{
		{c1, "source.txt", 2, "A\nB"},
		{c2, "source.txt", 1, "X"},
		{c2, "source.txt", 5, "C"},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
	if err := repo.AddedHunks(ctx, "no-such-ref", c2, func(AddedHunk) error { return nil }); err == nil {
		t.Fatal("want error for bad base")
	}
}
