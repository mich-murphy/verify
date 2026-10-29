package ship

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
)

func localGH(t *testing.T, script string) Options {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o700); err != nil { // #nosec G306 G703 -- fixed filename under t.TempDir; disposable fake executable.
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return Options{Repo: &gitx.Repo{Root: dir}}
}

func TestBattleGHDescendantPipeIsNotSuccess(t *testing.T) {
	ledger := os.Getenv("VERIFY_BATTLE_CHILD_LEDGER")
	if ledger == "" {
		ledger = t.TempDir()
	}
	pidFile := filepath.Join(ledger, "gh-child.pid")
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) { // #nosec G703 -- removes only the explicit disposable harness child registry.
		t.Fatal(err)
	}
	cleanup := func() {
		raw, err := os.ReadFile(pidFile) // #nosec G304 G703 -- explicit disposable harness child registry, never a repository path.
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err == nil && pid > 1 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		_ = os.Remove(pidFile) // #nosec G703 -- removes only the explicit disposable harness child registry.
	}
	t.Cleanup(cleanup)
	o := localGH(t, "/bin/sleep 30 &\necho $! > '"+pidFile+"'\nexit 0\n")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := gh(ctx, o, "pr", "view"); done <- err }()
	select {
	case err := <-done:
		t.Logf("gh outcome: %v", err)
		if err == nil {
			t.Fatal("gh with live pipe-owning descendant reported success")
		}
		raw, readErr := os.ReadFile(pidFile) // #nosec G304 G703 -- explicit disposable harness child registry, never a repository path.
		if readErr != nil {
			t.Fatalf("fixture child never started: %v", readErr)
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if parseErr != nil || pid <= 1 {
			t.Fatalf("invalid child registry: %q", raw)
		}
		ps := exec.CommandContext(t.Context(), "/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=") // #nosec G204 G702 -- fixed executable/options; PID parsed as positive int and formatted numerically.
		status, _ := ps.Output()
		if live := strings.TrimSpace(string(status)); live != "" && !strings.HasPrefix(live, "Z") {
			t.Fatalf("gh returned with live descendant %d (%s)", pid, live)
		}
	case <-time.After(3 * time.Second):
		// Independent watchdog ensures baseline cannot retain its sleep child.
		cleanup()
		select {
		case err := <-done:
			t.Logf("watchdog-released gh outcome: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("gh still blocked after explicit child cleanup")
		}
		t.Fatal("gh ignored cancelled context while descendant held capture pipes; watchdog cleanup required")
	}
}

func TestBattleGHOutputMustBeBounded(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			script := "/bin/dd if=/dev/zero bs=1024 count=2048 2>/dev/null"
			if stream == "stderr" {
				script = "(" + script + ") >&2"
			}
			o := localGH(t, script+"\n")
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			out, err := gh(ctx, o, "pr", "view")
			if err == nil {
				t.Fatalf("oversized %s accepted: captured %d stdout bytes", stream, len(out))
			}
			if len(out) != 0 {
				t.Fatalf("failed gh exposed partial output: %d bytes", len(out))
			}
		})
	}
}

func TestBattleGHEnvironmentAndErrorContract(t *testing.T) {
	t.Setenv("GH_HOST", "wrong.example")
	t.Setenv("GH_PROMPT_DISABLED", "0")
	t.Setenv("GH_NO_UPDATE_NOTIFIER", "0")
	t.Setenv("VERIFY_BATTLE_CALLER", "retained")
	o := localGH(t, `printf '%s|%s|%s|%s' "$GH_HOST" "$GH_PROMPT_DISABLED" "$GH_NO_UPDATE_NOTIFIER" "$VERIFY_BATTLE_CALLER"`)
	out, err := gh(t.Context(), o, "pr", "view")
	if err != nil || string(out) != "github.com|1|1|retained" {
		t.Fatalf("environment changed: %q %v", out, err)
	}
	o = localGH(t, "printf 'authentication fixture failed\\n' >&2\nexit 7\n")
	out, err = gh(t.Context(), o, "api")
	if out != nil || err == nil || err.Error() != "gh api: authentication fixture failed" {
		t.Fatalf("error contract: %q %v", out, err)
	}
}

func TestBattlePRViewOverflowStopsOrchestration(t *testing.T) {
	log := filepath.Join(t.TempDir(), "invocations")
	o := localGH(t, "printf '%s\\n' \"$*\" >> '"+log+"'\ncase \"$1 $2\" in\n'pr view') /bin/dd if=/dev/zero bs=1024 count=2048 2>/dev/null ;;\n*) echo unexpected-side-effect ;;\nesac\n")
	var rep Report
	_, err := prStep(t.Context(), o, &rep)
	if !errors.Is(err, errGHUnusable) {
		t.Fatalf("overflow not classified unusable: %v", err)
	}
	raw, readErr := os.ReadFile(log) // #nosec G304 -- test-owned invocation log.
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := strings.Fields(string(raw)); len(got) < 2 || got[0] != "pr" || got[1] != "view" {
		t.Fatalf("missing view invocation: %q", raw)
	}
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 1 {
		t.Fatalf("unreliable view triggered further gh invocation: %q", raw)
	}
	if rep.PRURL != "" {
		t.Fatalf("unreliable view published PR URL: %q", rep.PRURL)
	}
}

func TestBattlePRCancelledViewStopsOrchestration(t *testing.T) {
	log := filepath.Join(t.TempDir(), "invocations")
	o := localGH(t, "echo invoked >> '"+log+"'\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var rep Report
	_, err := prStep(ctx, o, &rep)
	if !errors.Is(err, errGHUnusable) {
		t.Fatalf("cancellation not classified unusable: %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("cancelled orchestration invoked gh: %v", err)
	}
}
