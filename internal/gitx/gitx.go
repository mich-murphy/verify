// Package gitx provides local, noninteractive Git inspection, diffing and
// push helpers. Revisions are resolved to immutable commit OIDs before use.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mich-murphy/verify/internal/runner"
)

// Repo identifies a working tree and the canonical Git common directory.
// Open is the only supported constructor; fields must not be changed by callers.
type Repo struct {
	Root        string
	CommonDir   string
	git         string
	commandOnce sync.Once
	commandGate chan struct{} // Serializes Git without delaying cancelled waiters.
	commands    gitCommands
	unsettled   atomic.Bool
}

// Open discovers a non-bare working tree without trusting inherited Git variables.
func Open(ctx context.Context, dir string) (*Repo, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: dir, git: git}
	root, err := r.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r.Root, err = filepath.EvalSymlinks(strings.TrimSuffix(string(root), "\n"))
	if err != nil {
		return nil, err
	}
	common, err := r.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	r.CommonDir, err = filepath.EvalSymlinks(strings.TrimSuffix(string(common), "\n"))
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repo) run(ctx context.Context, args ...string) ([]byte, error) {
	return r.runIn(ctx, r.Root, args...)
}

// runIn is run targeting dir (for example a Verify-owned worktree) instead of the repository root.
func (r *Repo) runIn(ctx context.Context, dir string, args ...string) ([]byte, error) {
	fixed := []string{"--no-pager", "--no-optional-locks", "--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.attributesFile=/dev/null", "-c", "core.autocrlf=false", "-c", "core.eol=lf", "-c", "core.symlinks=true", "-c", "diff.external=", "-c", "protocol.allow=never", "-c", "credential.helper=", "-c", "core.askPass=", "-C", dir}
	env := map[string]string{"PATH": os.Getenv("PATH"), "HOME": os.TempDir(), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_TERMINAL_PROMPT": "0", "GIT_PAGER": "cat", "LC_ALL": "C", "GIT_LFS_SKIP_SMUDGE": "1", "GIT_NO_REPLACE_OBJECTS": "1", "GIT_NO_LAZY_FETCH": "1"}
	return r.foreground(ctx, dir, append(fixed, args...), env, 5*time.Minute)
}

// ErrUnsettled requires explicit worktree recovery, never an automatic retry.
var ErrUnsettled = errors.New("git subprocess cleanup incomplete; repository execution quarantined")

type gitCommands interface {
	Run(context.Context, runner.Command) (runner.Result, error)
}

// Settled is sticky for the lifetime of Repo. Worktree owners must consult it
// at release time, including after errors swallowed by inspection helpers.
func (r *Repo) Settled() bool {
	return !r.unsettled.Load()
}

func (r *Repo) initCommandGate() {
	r.commandOnce.Do(func() { r.commandGate = make(chan struct{}, 1) })
}

func (r *Repo) foreground(ctx context.Context, dir string, args []string, env map[string]string, timeout time.Duration) ([]byte, error) {
	r.initCommandGate()
	select {
	case r.commandGate <- struct{}{}:
		defer func() { <-r.commandGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.unsettled.Load() {
		return nil, ErrUnsettled
	}
	if r.commands == nil {
		rn, err := runner.New(runner.Config{OutputLimit: 16 << 20})
		if err != nil {
			return nil, err
		}
		r.commands = rn
	}
	result, err := r.commands.Run(ctx, runner.Command{Args: append([]string{r.git}, args...), Dir: dir, Env: env, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	if !result.Settled {
		r.unsettled.Store(true)
		return nil, fmt.Errorf("%w: %s", errors.Join(ErrUnsettled, ctx.Err()), result.Error)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git: %w", ctx.Err())
	}
	if result.Stdout.Truncated || result.Stderr.Truncated {
		return nil, errors.New("git output exceeds 16 MiB")
	}
	if result.Status != runner.Pass {
		cause := result.ProcessError
		if cause == nil {
			cause = errors.New(result.Error)
		}
		return nil, fmt.Errorf("git: %w: %s", cause, result.Stderr.Output)
	}
	return []byte(result.Stdout.Output), nil
}

var oidPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Resolve returns the exact commit for a ref/revision, rejecting option injection.
func (r *Repo) Resolve(ctx context.Context, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
		return "", errors.New("invalid revision")
	}
	out, err := r.run(ctx, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if !oidPattern.MatchString(oid) {
		return "", errors.New("invalid commit OID")
	}
	return oid, nil
}

// Head returns the worktree's exact HEAD commit.
func (r *Repo) Head(ctx context.Context) (string, error) { return r.Resolve(ctx, "HEAD") }

// MergeBase compares complete commit histories, not just the final commit diff.
// Ambiguous criss-cross merge bases are rejected rather than arbitrarily selected.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	aa, err := r.Resolve(ctx, a)
	if err != nil {
		return "", err
	}
	bb, err := r.Resolve(ctx, b)
	if err != nil {
		return "", err
	}
	out, err := r.run(ctx, "merge-base", "--all", aa, bb)
	if err != nil {
		return "", err
	}
	bases := strings.Fields(string(out))
	if len(bases) != 1 {
		return "", errors.New("comparison requires exactly one merge base")
	}
	return bases[0], nil
}

// ShowFile reads a committed regular blob without invoking textconv or filters.
func (r *Repo) ShowFile(ctx context.Context, revision, path string) ([]byte, error) {
	oid, err := r.Resolve(ctx, revision)
	if err != nil {
		return nil, err
	}
	if !validPath(path) {
		return nil, errors.New("invalid repository-relative path")
	}
	meta, err := r.run(ctx, "ls-tree", "-z", oid, "--", path)
	if err != nil {
		return nil, err
	}
	entry := strings.SplitN(string(meta), "\t", 2)
	if len(entry) != 2 || strings.TrimSuffix(entry[1], "\x00") != path || !strings.HasPrefix(entry[0], "100") {
		return nil, errors.New("path is not a regular committed file")
	}
	return r.run(ctx, "cat-file", "blob", oid+":"+path)
}

func validPath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path && path != ".." && !strings.HasPrefix(path, "../") && !strings.ContainsAny(path, "\x00\r\n")
}

// Clean reports tracked, staged and nonignored untracked author changes.
// Ignored tool caches/build artifacts are not uncommitted source edits.
func (r *Repo) Clean(ctx context.Context) (bool, error) {
	out, err := r.run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	return len(out) == 0, err
}
