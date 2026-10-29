package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/runner"
)

func battleGit(t *testing.T, body string) *Repo {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	} // #nosec G306 -- Executable fixture script under t.TempDir.
	return &Repo{Root: dir, git: script}
}

func TestBattleGitDescendantsSettled(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, wait := range []bool{false, true} {
			t.Run(fmt.Sprintf("remote=%v/wait=%v", remote, wait), func(t *testing.T) {
				pidFile := filepath.Join(t.TempDir(), "child.pid")
				cleanup := func() {
					raw, err := os.ReadFile(pidFile) // #nosec G304 -- Explicit test-owned PID registry.
					if err != nil {
						return
					}
					pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
					if err == nil && pid > 1 {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
				t.Cleanup(cleanup)
				tail := "exit 0\n"
				if wait {
					tail = "wait \"$!\"\n"
				}
				r := battleGit(t, "/bin/sleep 30 &\necho $! > '"+pidFile+"'\n"+tail)
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var err error
					if remote {
						_, err = r.netGit(ctx, "ls-remote", "fixture", "refs/heads/main")
					} else {
						_, err = r.run(ctx, "status")
					}
					done <- err
				}()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("live descendant reported successful Git")
					}
					raw, err := os.ReadFile(pidFile) // #nosec G304 -- Explicit test-owned PID registry.
					if err != nil {
						t.Fatal(err)
					}
					pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
					if err != nil || pid <= 1 {
						t.Fatalf("bad child registry %q", raw)
					}
					ps := exec.CommandContext(t.Context(), "/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=") // #nosec G204 -- Fixed ps and parsed numeric fixture PID.
					out, _ := ps.Output()
					if status := strings.TrimSpace(string(out)); status != "" && !strings.HasPrefix(status, "Z") {
						t.Fatalf("descendant survived: %d %s", pid, status)
					}
				case <-time.After(5 * time.Second):
					cleanup()
					select {
					case err := <-done:
						t.Logf("watchdog cleanup result: %v", err)
					case <-time.After(time.Second):
						t.Error("command blocked after registered child cleanup")
					}
					t.Fatal("Git needed independent watchdog cleanup")
				}
			})
		}
	}
}

// Replaces the obsolete limitedBuffer unit test with the real command boundary.
func TestGitBufferExactBoundary(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, size := range []int{(16 << 20) - 1, 16 << 20, (16 << 20) + 1} {
			for _, stderr := range []bool{false, true} {
				t.Run(fmt.Sprintf("remote=%v/bytes=%d/stderr=%v", remote, size, stderr), func(t *testing.T) {
					body := fmt.Sprintf("/usr/bin/head -c %d /dev/zero", size)
					if stderr {
						body += " >&2"
					}
					r := battleGit(t, body+"\n")
					var out []byte
					var err error
					if remote {
						out, err = r.netGit(t.Context(), "fixture")
					} else {
						out, err = r.run(t.Context(), "fixture")
					}
					if size > 16<<20 {
						if err == nil || out != nil {
							t.Fatalf("overflow accepted: bytes=%d err=%v", len(out), err)
						}
					} else if err != nil || (!stderr && len(out) != size) || (stderr && len(out) != 0) {
						t.Fatalf("exact boundary: bytes=%d err=%v", len(out), err)
					}
				})
			}
		}
	}
}

func TestBattleGitExitStartupAndCancellation(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, code := range []int{1, 2} {
			r := battleGit(t, fmt.Sprintf("exit %d\n", code))
			var err error
			if remote {
				_, err = r.netGit(t.Context(), "fixture")
			} else {
				_, err = r.run(t.Context(), "fixture")
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != code {
				t.Fatalf("remote=%v exit=%d: %v", remote, code, err)
			}
		}
	}
	r := battleGit(t, "exec /bin/sleep 30\n")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := r.run(ctx, "fixture"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost cancellation: %v", err)
	}
	r.git = filepath.Join(t.TempDir(), "missing")
	if _, err := r.run(t.Context(), "fixture"); err == nil {
		t.Fatal("startup failure passed")
	}
	if !r.Settled() {
		t.Fatal("failed startup quarantined settled Repo")
	}
}

type battleGitCommands struct {
	calls int
	run   func(int, runner.Command) runner.Result
}

func (c *battleGitCommands) Run(_ context.Context, cmd runner.Command) (runner.Result, error) {
	c.calls++
	return c.run(c.calls, cmd), nil
}

func TestBattleGitUnsettledBlocksDestructiveReset(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, "wt")
	if err := os.Mkdir(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".git", "preserve"} {
		if err := os.WriteFile(filepath.Join(wt, name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const oid = "1111111111111111111111111111111111111111"
	calls := &battleGitCommands{run: func(n int, _ runner.Command) runner.Result {
		if n == 1 {
			return runner.Result{Status: runner.Pass, Settled: true, Stdout: runner.Evidence{Output: oid + "\n"}}
		}
		return runner.Result{Status: runner.Fail, Settled: false, Error: "injected incomplete cleanup"}
	}}
	r := &Repo{Root: dir, git: "fixture", commands: calls}
	if err := r.ResetWorktree(t.Context(), wt, oid, nil); !errors.Is(err, ErrUnsettled) {
		t.Fatalf("reset lost quarantine: %v", err)
	}
	if r.Settled() {
		t.Fatal("incomplete cleanup not sticky")
	}
	if _, err := os.Stat(filepath.Join(wt, "preserve")); err != nil {
		t.Fatalf("destructive cleanup after swallowed inspection error: %v", err)
	}
	if _, err := r.run(t.Context(), "later"); !errors.Is(err, ErrUnsettled) {
		t.Fatalf("later command admitted: %v", err)
	}
	if err := r.RemoveWorktree(t.Context(), wt); !errors.Is(err, ErrUnsettled) {
		t.Fatalf("remove lost quarantine: %v", err)
	}
	if calls.calls != 2 {
		t.Fatalf("subprocess after quarantine: %d", calls.calls)
	}
	if _, err := os.Stat(filepath.Join(wt, "preserve")); err != nil {
		t.Fatalf("remove fallback deleted quarantined worktree: %v", err)
	}
}

func TestBattleGitEnvironmentAndArguments(t *testing.T) {
	t.Setenv("VERIFY_GIT_FIXTURE", "inherited")
	t.Setenv("GIT_DIR", "redirect")
	t.Setenv("GIT_INDEX_FILE", "redirect-index")
	t.Setenv("GIT_SSH_COMMAND", "fixture-ssh")
	r := battleGit(t, `printf '%s|%s|%s|%s|%s' "$VERIFY_GIT_FIXTURE" "$GIT_DIR" "$GIT_INDEX_FILE" "$GIT_SSH_COMMAND" "$GIT_TERMINAL_PROMPT"`+"\n")
	out, err := r.run(t.Context(), "fixture")
	if err != nil || string(out) != "||||0" {
		t.Fatalf("local environment: %q %v", out, err)
	}
	out, err = r.netGit(t.Context(), "fixture")
	if err != nil || string(out) != "inherited|||fixture-ssh|0" {
		t.Fatalf("remote environment: %q %v", out, err)
	}
	seam := &battleGitCommands{run: func(_ int, cmd runner.Command) runner.Result {
		joined := strings.Join(cmd.Args, "\n")
		if !strings.HasSuffix(joined, "\n--\nweird path\tname") || !strings.Contains(joined, "core.hooksPath=/dev/null") || !strings.Contains(joined, "protocol.allow=never") {
			t.Errorf("hardened argv changed: %q", cmd.Args)
		}
		return runner.Result{Status: runner.Pass, Settled: true}
	}}
	r.commands = seam
	if _, err := r.run(t.Context(), "show", "--", "weird path\tname"); err != nil {
		t.Fatal(err)
	}
}

func TestBattleGitMarkerReleaseObservesLaterSettlement(t *testing.T) {
	for _, settled := range []bool{true, false} {
		t.Run(fmt.Sprint(settled), func(t *testing.T) {
			wt := filepath.Join(t.TempDir(), "wt")
			release, err := BeginWorktreeUse(wt)
			if err != nil {
				t.Fatal(err)
			}
			r := &Repo{Root: filepath.Dir(wt), git: "fixture"}
			if !r.Settled() {
				t.Fatal("new repo not settled")
			}
			if !settled {
				r.commands = &battleGitCommands{run: func(_ int, _ runner.Command) runner.Result {
					return runner.Result{Status: runner.Fail, Settled: false, Error: "late identity inspection failure"}
				}}
				if err := r.AssertWorktreeIdentity(t.Context(), wt, "fixture"); !errors.Is(err, ErrUnsettled) {
					t.Fatal(err)
				}
				if _, err := r.run(t.Context(), "later"); !errors.Is(err, ErrUnsettled) {
					t.Fatal(err)
				}
			}
			err = r.ReleaseWorktreeUse(release, true)
			if settled {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(WorktreeQuarantine(wt)); !os.IsNotExist(err) {
					t.Fatalf("settled marker retained: %v", err)
				}
			} else {
				if !errors.Is(err, ErrUnsettled) {
					t.Fatalf("late failure lost: %v", err)
				}
				if _, err := os.Stat(WorktreeQuarantine(wt)); err != nil {
					t.Fatalf("late failure released marker: %v", err)
				}
			}
		})
	}
}

func TestBattleGitCancelledWaiterDoesNotStart(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	seam := &battleGitCommands{run: func(_ int, _ runner.Command) runner.Result {
		close(entered)
		<-release
		return runner.Result{Status: runner.Pass, Settled: true}
	}}
	r := &Repo{Root: t.TempDir(), git: "fixture", commands: seam}
	done := make(chan error, 1)
	go func() { _, err := r.run(t.Context(), "first"); done <- err }()
	<-entered
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.run(ctx, "must not start"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission: %v", err)
	}
}

func TestBattleGitRemoteTargetArgvUnchanged(t *testing.T) {
	const oid = "1111111111111111111111111111111111111111"
	const destination = "ssh://fixture.invalid/repo.git"
	r := &Repo{Root: t.TempDir(), git: "fixture"}
	r.commands = &battleGitCommands{run: func(_ int, cmd runner.Command) runner.Result {
		want := []string{"fixture", "-C", r.Root, "push", "--force-with-lease=refs/heads/topic:" + oid, destination, oid + ":refs/heads/topic"}
		if !slices.Equal(cmd.Args, want) {
			t.Errorf("remote target/argv changed: %q want %q", cmd.Args, want)
		}
		return runner.Result{Status: runner.Pass, Settled: true}
	}}
	if err := r.PushLeaseToURL(t.Context(), destination, "topic", oid, oid); err != nil {
		t.Fatal(err)
	}
}

func TestBattleGitCancellationIdentitySurvivesOtherFailures(t *testing.T) {
	for _, settled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		r := &Repo{Root: t.TempDir(), git: "fixture"}
		r.commands = &battleGitCommands{run: func(_ int, _ runner.Command) runner.Result {
			cancel()
			return runner.Result{Status: runner.Cancelled, Settled: settled, Stdout: runner.Evidence{Truncated: true}, Error: "cancelled with concurrent failure"}
		}}
		_, err := r.run(ctx, "fixture")
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation identity lost: settled=%v err=%v", settled, err)
		}
		if errors.Is(err, ErrUnsettled) == settled {
			t.Fatalf("quarantine identity lost: settled=%v err=%v", settled, err)
		}
	}
}
