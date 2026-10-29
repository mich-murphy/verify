package gitx

import (
	"context"
	"slices"
	"strings"
)

// WorktreeChanges lists the paths (relative to the repository root, sorted)
// that differ in the working tree: tracked files changed against HEAD, staged
// or unstaged, excluding deletions, plus untracked files that are not ignored.
func (r *Repo) WorktreeChanges(ctx context.Context) ([]string, error) {
	tracked, err := r.run(ctx, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "--diff-filter=d", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	untracked, err := r.run(ctx, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, raw := range [][]byte{tracked, untracked} {
		for p := range strings.SplitSeq(string(raw), "\x00") {
			if p != "" {
				paths = append(paths, p)
			}
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}
