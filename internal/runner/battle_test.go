package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBattleFloodHelper(t *testing.T) {
	if os.Getenv("VERIFY_BATTLE_FLOOD") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	for range 128 {
		if _, err := os.Stdout.Write(chunk); err != nil {
			os.Exit(91)
		}
		if _, err := os.Stderr.Write(chunk); err != nil {
			os.Exit(92)
		}
	}
	time.Sleep(time.Hour)
	os.Exit(93)
}

func TestBattleFloodTimeoutEscalatesAndBoundsBothStreams(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := testRunner(t, Config{OutputLimit: 257, KillGrace: 10 * time.Millisecond})
	got := runCommand(t, r, Command{Args: []string{exe, "-test.run=^TestBattleFloodHelper$"}, Dir: t.TempDir(), Timeout: time.Second, Env: map[string]string{"VERIFY_BATTLE_FLOOD": "1", "GORACE": "atexit_sleep_ms=0", "GOCOVERDIR": t.TempDir()}})
	if got.Status != TimedOut || !got.Settled || got.ExitCode != -1 {
		t.Fatalf("timeout did not kill and settle ignoring child: %+v", got)
	}
	for _, ev := range []Evidence{got.Stdout, got.Stderr} {
		if len(ev.Output) != 257 || !ev.Truncated || ev.Bytes != 4<<20 {
			t.Fatalf("bounded flood evidence: %+v", ev)
		}
	}
	if next := runCommand(t, r, helperTask(t, "ok")); next.Status != Pass {
		t.Fatalf("settled timeout quarantined runner: %+v", next)
	}
}

func TestBattleStartupFailuresAreNotObservedExits(t *testing.T) {
	dir := t.TempDir()
	nonexec := filepath.Join(dir, "nonexec")
	if err := os.WriteFile(nonexec, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{dir}, {nonexec}, {"/missing/verify-battle-command"}, {"/bin/sh", "-c", "exit 127"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			got := runCommand(t, testRunner(t, Config{}), Command{Args: args, Dir: dir, Timeout: time.Second})
			realExit := args[0] == "/bin/sh"
			if got.Status != Fail || !got.Settled || got.Started.IsZero() == realExit {
				t.Fatalf("startup/exit distinction lost: %+v", got)
			}
			if realExit && got.ExitCode != 127 || !realExit && got.ExitCode != -1 {
				t.Fatalf("wrong observed exit: %+v", got)
			}
		})
	}
}

func TestBattlePreCancelledNeverStarts(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := testRunner(t, Config{})
	got, err := r.Run(ctx, helperTask(t, "ok"))
	if err != nil || got.Status != Cancelled || !got.Started.IsZero() || !got.Settled {
		t.Fatalf("pre-cancelled result: %+v %v", got, err)
	}
}

func BenchmarkBattleCapture(b *testing.B) {
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	b.ReportAllocs()
	b.SetBytes(4 << 20)
	for b.Loop() {
		c := newCapture(1 << 20)
		for range 128 {
			_, _ = c.Write(chunk)
		}
		_ = c.evidence()
	}
}

// Includes process startup, wait, and the runner's owned-group settlement check.
func BenchmarkBattleForegroundTrue(b *testing.B) {
	r, err := New(Config{})
	if err != nil {
		b.Fatal(err)
	}
	cmd := Command{Args: []string{"/usr/bin/true"}, Dir: b.TempDir(), Timeout: time.Second}
	b.ReportAllocs()
	for b.Loop() {
		got, err := r.Run(b.Context(), cmd)
		if err != nil || got.Status != Pass || !got.Settled {
			b.Fatalf("%+v %v", got, err)
		}
	}
}

func TestBattleProcessErrorNeverMasksLifecycleFailure(t *testing.T) {
	r := testRunner(t, Config{})
	ordinary := runCommand(t, r, helperTask(t, "fail"))
	var exit *exec.ExitError
	if !errors.As(ordinary.ProcessError, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("ordinary exit cause: %+v", ordinary)
	}
	r.process.probe = func(int) error { return nil }
	r.process.table = func(context.Context) ([]byte, error) { return nil, errors.New("injected inspection failure") }
	failed := runCommand(t, r, helperTask(t, "fail"))
	if failed.Settled || failed.ProcessError != nil {
		t.Fatalf("lifecycle failure exposed normal exit cause: %+v", failed)
	}
	if _, err := r.Run(t.Context(), helperTask(t, "ok")); err == nil {
		t.Fatal("runner reused after failed inspection")
	}
}
