package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AssertWorktreeIdentity rejects any change to HEAD, the index, or tracked
// content after running repository code. Ignored build products remain allowed.
func (r *Repo) AssertWorktreeIdentity(ctx context.Context, dir, sha string) error {
	head, err := r.runIn(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(head)) != sha {
		return fmt.Errorf("worktree HEAD differs from %s", sha)
	}
	for _, args := range [][]string{{"diff", "--quiet", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--cached", "HEAD", "--"}, {"diff", "--quiet", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--"}} {
		if _, err := r.runIn(ctx, dir, args...); err != nil {
			return fmt.Errorf("tracked worktree/index differs from %s: %w", sha, err)
		}
	}
	return nil
}

// WorktreeQuarantine persists in-progress ownership and incomplete cleanup across
// Gate invocations. A marker left by a crash requires explicit human recovery.
func WorktreeQuarantine(path string) string { return path + ".quarantine" }

func CheckWorktreeQuarantine(path string) error {
	_, err := os.Lstat(WorktreeQuarantine(path))
	if err == nil {
		return fmt.Errorf("worktree has an in-progress or incomplete-cleanup marker: %s; inspect owned processes, then remove marker explicitly", WorktreeQuarantine(path))
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// BeginWorktreeUse creates a durable marker before the first worktree command.
// Call the returned release only after *all* processes have settled. Creation
// uses O_EXCL: neither a stale marker nor another owner's marker is adopted.
// Release checks the marker's file identity before removing it.
func BeginWorktreeUse(path string) (release func() error, err error) {
	marker := WorktreeQuarantine(path)
	// #nosec G304 -- Marker path derives from a locked Gate-owned worktree path.
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err = f.WriteString("worktree in use; if Gate exited unexpectedly, inspect owned processes before removing this marker\n"); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = syncMarkerDir(marker); err != nil {
		return nil, err
	}
	return func() error {
		current, statErr := os.Lstat(marker)
		if statErr != nil {
			return statErr
		}
		if !os.SameFile(info, current) {
			return fmt.Errorf("worktree marker was replaced; refusing to remove %s", marker)
		}
		if err := os.Remove(marker); err != nil {
			return err
		}
		return syncMarkerDir(marker)
	}, nil
}

func syncMarkerDir(marker string) error {
	d, err := os.Open(filepath.Dir(marker))
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
