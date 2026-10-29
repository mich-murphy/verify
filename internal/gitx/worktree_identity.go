package gitx

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
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
// Verify invocations. A marker left by a crash requires explicit human recovery.
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
// Release checks both file identity and a unique persisted ownership token.
func BeginWorktreeUse(path string) (release func() error, err error) {
	marker := WorktreeQuarantine(path)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	content := fmt.Sprintf("worktree in use; if Verify exited unexpectedly, inspect owned processes before removing this marker\nowner: %x\n", nonce)
	// #nosec G304 -- Marker path derives from a locked Verify-owned worktree path.
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err = f.WriteString(content); err != nil {
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
		if err := checkOwnedMarker(marker, info, content); err != nil {
			return err
		}
		if err := os.Remove(marker); err != nil {
			return err
		}
		return syncMarkerDir(marker)
	}, nil
}

func checkOwnedMarker(marker string, info os.FileInfo, content string) error {
	current, err := os.Lstat(marker)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return fmt.Errorf("worktree marker was replaced; refusing to remove %s", marker)
	}
	// #nosec G304 -- Marker path derives from a locked Verify-owned worktree path.
	f, err := os.Open(marker)
	if err != nil {
		return err
	}
	opened, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return statErr
	}
	data, readErr := io.ReadAll(io.LimitReader(f, int64(len(content)+1)))
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	current, err = os.Lstat(marker)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(info, current) || string(data) != content {
		return fmt.Errorf("worktree marker was replaced or changed; refusing to remove %s", marker)
	}
	return nil
}

func syncMarkerDir(marker string) error {
	d, err := os.Open(filepath.Dir(marker))
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// ReleaseWorktreeUse releases an owned marker only when both repository commands
// and Git cleanup settled. Git settlement is checked at release time under the
// command lock, so a later failure cannot be cleared by an earlier observation.
func (r *Repo) ReleaseWorktreeUse(release func() error, commandsSettled bool) error {
	r.initCommandGate()
	r.commandGate <- struct{}{}
	defer func() { <-r.commandGate }()
	if r.unsettled.Load() {
		return ErrUnsettled
	}
	if !commandsSettled {
		return nil
	}
	return release()
}
