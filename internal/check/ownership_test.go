package check

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/state"
)

// The child is a separate Verify-like process running prepare. The parent kills
// it without allowing deferred cleanup, exercising persistent ownership.
func TestCheckAbruptParentDeath(t *testing.T) {
	if os.Getenv("VERIFY_ABRUPT_CHILD") == "1" {
		r, err := gitx.Open(t.Context(), os.Getenv("VERIFY_ABRUPT_REPO"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Run(t.Context(), r, Options{}); err != nil {
			t.Fatal(err)
		}
		return
	}
	fakePrek(t)
	pidFile := filepath.Join(t.TempDir(), "owned-pid")
	t.Setenv("VERIFY_ABRUPT_PID_FILE", pidFile)
	yaml := `prepare:
  - name: stall
    run: [sh, -c, 'echo $$ > "$VERIFY_ABRUPT_PID_FILE.tmp"; mv "$VERIFY_ABRUPT_PID_FILE.tmp" "$VERIFY_ABRUPT_PID_FILE"; sleep 30']
`
	repo := newRepo(t, yaml, true)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- Test-owned binary and fixed test entrypoint.
	child := exec.CommandContext(t.Context(), exe, "-test.run=^TestCheckAbruptParentDeath$")
	child.Env = append(os.Environ(), "VERIFY_ABRUPT_CHILD=1", "VERIFY_ABRUPT_REPO="+repo.Root)
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var group int
	for group == 0 {
		select {
		case <-deadline.C:
			t.Fatal("prepare did not start before timeout")
		case <-ticker.C:
			// #nosec G304 -- PID file is confined to this test's temporary directory.
			raw, readErr := os.ReadFile(pidFile)
			if readErr == nil {
				group, err = strconv.Atoi(strings.TrimSpace(string(raw)))
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	defer func() { _ = syscall.Kill(-group, syscall.SIGKILL) }()
	marker := gitx.WorktreeQuarantine(filepath.Join(state.Open(repo.CommonDir).WorktreesDir(), "check"))
	if _, err = os.Stat(marker); err != nil {
		t.Fatalf("running subprocess had no durable marker: %v", err)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("abrupt child unexpectedly passed")
	}
	if err = syscall.Kill(-group, 0); err != nil {
		t.Fatalf("owned subprocess did not survive parent death: %v", err)
	}
	if _, err = Run(context.Background(), repo, Options{}); err == nil || !strings.Contains(err.Error(), "marker") {
		t.Fatalf("orphaned worktree reused: %v", err)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatalf("stale marker disappeared: %v", err)
	}
	if err = syscall.Kill(-group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatal(err)
	}
	// Leave the marker in this disposable repository: recovery must not be
	// claimed before the orphan is observed to have fully settled.
}

func TestCheckMarkerClearsOnSettledPassAndFailure(t *testing.T) {
	fakePrek(t)
	for _, tc := range []struct{ name, prepare, expected string }{
		{"pass", "test -f \"$PWD.quarantine\"", "pass"},
		{"failed settled", "test -f \"$PWD.quarantine\" && exit 7", "fail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t, "prepare:\n  - {name: probe, run: [sh, -c, '"+tc.prepare+"']}\n", true)
			outcome, err := Run(t.Context(), repo, Options{})
			if err != nil || outcome.Record.Status != tc.expected {
				t.Fatalf("%v %+v", err, outcome)
			}
			marker := gitx.WorktreeQuarantine(filepath.Join(state.Open(repo.CommonDir).WorktreesDir(), "check"))
			if _, err = os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatalf("settled run retained marker: %v", err)
			}
		})
	}
}

func TestCheckMarkerExistsDuringPrekAudit(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then test -f \"$(pwd -P).quarantine\" || exit 44; echo '[]'; exit 0; fi\nexit 0\n"
	// #nosec G306 -- Test-owned fake prek must be executable.
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := newRepo(t, head, true)
	out, err := Run(t.Context(), repo, Options{})
	if err != nil || out.Record.Status != "pass" {
		t.Fatalf("audit ran without ownership marker: %v %+v", err, out)
	}
}
