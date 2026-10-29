package gitx

import (
	"context"
	"fmt"
	"strings"
)

// AddedHunk is one run of consecutive added lines in one file of one commit.
type AddedHunk struct {
	Commit    string
	Path      string
	StartLine int // new-file line of the first added line
	Text      string
}

// AddedHunks calls yield for every hunk of added lines in each commit of
// base..sha, oldest first. Each commit is diffed on its own against its first
// parent (root commits against the empty tree), so content added in one
// commit and removed in a later one is still reported. Binary files carry no
// text hunks and are skipped. Any git or parse error stops the walk.
func (r *Repo) AddedHunks(ctx context.Context, base, sha string, yield func(AddedHunk) error) error {
	b, err := r.Resolve(ctx, base)
	if err != nil {
		return err
	}
	s, err := r.Resolve(ctx, sha)
	if err != nil {
		return err
	}
	list, err := r.run(ctx, "rev-list", "--reverse", "--parents", b+".."+s, "--")
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(list), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		commit := f[0]
		var out []byte
		if len(f) == 1 {
			out, err = r.run(ctx, "diff-tree", "-p", "--root", "--no-commit-id", "--unified=0", "--no-color", "--no-renames", "--no-ext-diff", "--no-textconv", commit, "--")
		} else {
			out, err = r.run(ctx, "diff", "--unified=0", "--no-color", "--no-renames", "--no-ext-diff", "--no-textconv", f[1], commit, "--")
		}
		if err != nil {
			return err
		}
		d, err := ParseLineDiff(string(out))
		if err != nil {
			return fmt.Errorf("commit %s: %w", commit, err)
		}
		if err := yieldHunks(commit, d.Added, yield); err != nil {
			return err
		}
	}
	return nil
}

// yieldHunks groups added lines into hunks. Separate unified=0 hunks are
// always separated by an unchanged line, so consecutive numbers in one path
// belong to one hunk.
func yieldHunks(commit string, lines []Line, yield func(AddedHunk) error) error {
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j].Path == lines[i].Path && lines[j].Number == lines[j-1].Number+1 {
			j++
		}
		text := make([]string, 0, j-i)
		for _, l := range lines[i:j] {
			text = append(text, l.Text)
		}
		h := AddedHunk{Commit: commit, Path: lines[i].Path, StartLine: lines[i].Number, Text: strings.Join(text, "\n")}
		if err := yield(h); err != nil {
			return err
		}
		i = j
	}
	return nil
}

// YieldWorkingHunks groups working-tree additions for the fast secret guard.
func YieldWorkingHunks(sha string, lines []Line, yield func(AddedHunk) error) error {
	return yieldHunks(sha, lines, yield)
}
