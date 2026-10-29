package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Change is one file changed between two commits, with line counts.
// Binary files count as zero lines.
type Change struct {
	Path           string
	Added, Deleted int
	Binary         bool
}

// Changes lists files changed from base to sha (no rename detection).
func (r *Repo) Changes(ctx context.Context, base, sha string) ([]Change, error) {
	b, err := r.Resolve(ctx, base)
	if err != nil {
		return nil, err
	}
	s, err := r.Resolve(ctx, sha)
	if err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", b, s, "--")
	if err != nil {
		return nil, err
	}
	var changes []Change
	for rec := range strings.SplitSeq(string(out), "\x00") {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			return nil, errors.New("unparseable git numstat record")
		}
		add, _ := strconv.Atoi(parts[0]) // "-" for binary files yields 0
		del, _ := strconv.Atoi(parts[1])
		changes = append(changes, Change{Path: parts[2], Added: add, Deleted: del, Binary: parts[0] == "-" || parts[1] == "-"})
	}
	return changes, nil
}

// Branch returns the current branch name, or "" when HEAD is detached.
func (r *Repo) Branch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", nil //nolint:nilerr // symbolic-ref exits non-zero for a detached HEAD.
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrNonRegularFile distinguishes an existing nonregular entry from an absent file.
// Policy readers must fail closed; header-only inspectors may deliberately skip it.
var ErrNonRegularFile = errors.New("path is not a regular committed file")

// FileAt reads a regular file at a revision; only an absent entry returns no error with found=false.
func (r *Repo) FileAt(ctx context.Context, revision, path string) (data []byte, found bool, err error) {
	oid, err := r.Resolve(ctx, revision)
	if err != nil {
		return nil, false, err
	}
	if !validPath(path) {
		return nil, false, errors.New("invalid repository-relative path")
	}
	meta, err := r.run(ctx, "ls-tree", "-z", oid, "--", path)
	if err != nil {
		return nil, false, err
	}
	if len(meta) == 0 {
		return nil, false, nil
	}
	// Never reinterpret an existing symlink/submodule/directory as absent policy.
	entry := strings.SplitN(string(meta), "\t", 2)
	if len(entry) != 2 || strings.TrimSuffix(entry[1], "\x00") != path {
		return nil, false, errors.New("unparseable committed file metadata")
	}
	if !strings.HasPrefix(entry[0], "100") {
		return nil, false, ErrNonRegularFile
	}
	// The immutable commit, exact path and regular-file metadata are validated
	// above; do not resolve and inspect the same entry again through ShowFile.
	data, err = r.run(ctx, "cat-file", "blob", oid+":"+path)
	return data, err == nil, err
}

// AddWorktree creates a detached worktree of sha at path (which must not exist).
func (r *Repo) AddWorktree(ctx context.Context, path, sha string) error {
	oid, err := r.Resolve(ctx, sha)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(parentDir(path), 0o700); err != nil {
		return err
	}
	_, err = r.run(ctx, "worktree", "add", "--detach", path, oid)
	return err
}

// ResetWorktree makes path a detached worktree of sha with exactly the
// commit's tracked files, keeping only the ignored/untracked names in keep
// (for example node_modules). A missing or invalid worktree is recreated.
func (r *Repo) ResetWorktree(ctx context.Context, path, sha string, keep []string) error {
	oid, err := r.Resolve(ctx, sha)
	if err != nil {
		return err
	}
	if !r.validWorktree(ctx, path) {
		if err = r.removeWorktreePath(path); err != nil {
			return err
		}
		if _, err = r.run(ctx, "worktree", "prune"); err != nil {
			return err
		}
		return r.AddWorktree(ctx, path, oid)
	}
	if _, err = r.runIn(ctx, path, "checkout", "--detach", "--force", oid); err != nil {
		return err
	}
	clean := []string{"clean", "-ffdx"}
	for _, k := range keep {
		clean = append(clean, "-e", k)
	}
	_, err = r.runIn(ctx, path, clean...)
	return err
}

// validWorktree reports whether path is the top level of a worktree of this repository.
func (r *Repo) validWorktree(ctx context.Context, path string) bool {
	if fi, err := os.Lstat(filepath.Join(path, ".git")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	top, err := r.runIn(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	common, err := r.runIn(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false
	}
	topReal, err1 := filepath.EvalSymlinks(strings.TrimSuffix(string(top), "\n"))
	commonReal, err2 := filepath.EvalSymlinks(strings.TrimSuffix(string(common), "\n"))
	return err1 == nil && err2 == nil && topReal == real && commonReal == r.CommonDir
}

// RemoveWorktree force-removes a worktree created by AddWorktree and prunes
// stale administrative entries.
func (r *Repo) RemoveWorktree(ctx context.Context, path string) error {
	_, err := r.run(ctx, "worktree", "remove", "--force", path)
	if err != nil {
		err = errors.Join(err, r.removeWorktreePath(path))
		if !r.Settled() {
			return errors.Join(err, ErrUnsettled)
		}
	}
	_, perr := r.run(ctx, "worktree", "prune")
	return errors.Join(err, perr)
}

// Keep cleanup admission and deletion atomic with respect to Git quarantine.
func (r *Repo) removeWorktreePath(path string) error {
	r.initCommandGate()
	r.commandGate <- struct{}{}
	defer func() { <-r.commandGate }()
	if r.unsettled.Load() {
		return ErrUnsettled
	}
	return os.RemoveAll(path)
}

func parentDir(p string) string {
	if i := strings.LastIndexByte(p, os.PathSeparator); i > 0 {
		return p[:i]
	}
	return "."
}
