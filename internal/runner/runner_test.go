package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Real fixture command. No shell interprets task arguments or creates children.
func TestRunnerHelper(t *testing.T) {
	if os.Getenv("RUNNER_HELPER") != "1" {
		return
	}
	args := os.Args
	at := 0
	for i, a := range args {
		if a == "--" {
			at = i + 1
			break
		}
	}
	if at == 0 || at >= len(args) {
		os.Exit(91)
	}
	switch args[at] {
	case "ownpid":
		// #nosec G703 -- Parent supplies a path confined to t.TempDir.
		if err := os.WriteFile(args[at+1], []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(97)
		}
	case "ok":
		fmt.Print("ok")
	case "fail":
		os.Exit(7)
	case "delayedfail":
		time.Sleep(200 * time.Millisecond)
		os.Exit(7)
	case "sleep":
		d, err := time.ParseDuration(args[at+1])
		if err != nil {
			os.Exit(92)
		}
		time.Sleep(d)
	case "output":
		fmt.Print(strings.Repeat("x", 4096))
		fmt.Fprint(os.Stderr, strings.Repeat("y", 2048))
	case "env":
		fmt.Printf("SECRET=%s;APP=%s;PATH=%s;ARG=%s", os.Getenv("SEEDED_SECRET"), os.Getenv("APP"), os.Getenv("PATH"), args[at+1])
	case "orphan", "orphanquiet", "tree":
		exe, err := os.Executable()
		if err != nil {
			os.Exit(93)
		}
		childMode := "sleep"
		childArg := "30s"
		if args[at] == "orphan" {
			childMode = "delayedfail"
			childArg = "unused"
		}
		// #nosec G204 -- Self-executable fixture process with explicit argv, no shell.
		child := exec.CommandContext(t.Context(), exe, "-test.run=^TestRunnerHelper$", "--", childMode, childArg)
		child.Env = os.Environ()
		if args[at] != "orphanquiet" {
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
		}
		if err = child.Start(); err != nil {
			os.Exit(94)
		}
		// #nosec G703 -- Helper-only PID path is supplied by its parent from t.TempDir.
		if err = os.WriteFile(args[at+1], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(95)
		}
		if args[at] == "tree" {
			time.Sleep(30 * time.Second)
		}
	default:
		os.Exit(96)
	}
	os.Exit(0)
}

func helperTask(t *testing.T, mode string, args ...string) Command {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{exe, "-test.run=^TestRunnerHelper$", "--", mode}
	argv = append(argv, args...)
	return Command{Args: argv, Dir: t.TempDir(), Timeout: 5 * time.Second, Env: map[string]string{"RUNNER_HELPER": "1", "GORACE": "atexit_sleep_ms=0", "GOCOVERDIR": t.TempDir()}}
}

func testRunner(t *testing.T, c Config) *Runner {
	t.Helper()
	r, err := New(c)
	if err != nil || r == nil {
		t.Fatalf("New(valid config) must return a non-nil runner and no error: %v %v", r, err)
	}
	return r
}

func runCommand(t *testing.T, r *Runner, c Command) Result {
	t.Helper()
	res, err := r.Run(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSingleCommandSafety(t *testing.T) {
	r := testRunner(t, Config{OutputLimit: 128, Inherit: []string{"PATH"}})
	fail := runCommand(t, r, helperTask(t, "fail"))
	if fail.Status != Fail || fail.ExitCode != 7 || fail.Started.IsZero() {
		t.Fatal(fail)
	}
	output := runCommand(t, r, helperTask(t, "output"))
	if output.Status != Pass || !output.Stdout.Truncated || output.Stdout.Bytes != 4096 || len(output.Stdout.Output) != 128 || !output.Stderr.Truncated {
		t.Fatal(output)
	}
	t.Setenv("SEEDED_SECRET", "do-not-leak")
	c := helperTask(t, "env", "$(echo injected); *")
	c.Env["APP"] = "override"
	got := runCommand(t, r, c)
	if got.Status != Pass || !strings.Contains(got.Stdout.Output, "SECRET=;APP=override;") {
		t.Fatal(got)
	}
	c.Args = []string{"nonexistent gate executable"}
	got = runCommand(t, r, c)
	if got.Status != Fail || !got.Started.IsZero() || got.ExitCode != -1 {
		t.Fatal(got)
	}
}

func TestNativeStdinAndReal127(t *testing.T) {
	r := testRunner(t, Config{Inherit: []string{"PATH"}})
	c := Command{Args: []string{"/bin/sh", "-c", "cat; exit 127"}, Dir: t.TempDir(), Timeout: time.Second, Stdin: []byte("hello")}
	got := runCommand(t, r, c)
	if got.Status != Fail || got.ExitCode != 127 || got.Started.IsZero() || got.Stdout.Output != "hello" {
		t.Fatal(got)
	}
}

func TestTimeoutCancellationAndOrphans(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			c := helperTask(t, "tree", pidFile)
			c.Timeout = 300 * time.Millisecond
			ctx := t.Context()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				go func() { time.Sleep(150 * time.Millisecond); cancel() }()
			}
			if mode == "orphan" {
				c.Args[len(c.Args)-2] = "orphan"
				c.Timeout = 5 * time.Second
			}
			got, err := testRunner(t, Config{KillGrace: 20 * time.Millisecond}).Run(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			want := TimedOut
			if mode == "cancel" {
				want = Cancelled
			}
			if mode == "orphan" {
				want = Fail
			}
			if got.Status != want || !got.Settled {
				t.Fatal(got)
			}
			// #nosec G304 -- PID file created inside the test temporary directory.
			data, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			// #nosec G204 -- Fixed process listing with parsed numeric PID only.
			ps, err := exec.CommandContext(t.Context(), "/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
			if err == nil && !strings.HasPrefix(strings.TrimSpace(string(ps)), "Z") {
				t.Fatalf("child survived: %s", ps)
			}
		})
	}
}

func TestInvalidCommandAndQuarantine(t *testing.T) {
	r := testRunner(t, Config{})
	good := helperTask(t, "ok")
	for _, change := range []func(*Command){func(c *Command) { c.Args = nil }, func(c *Command) { c.Dir = "" }, func(c *Command) { c.Timeout = 0 }, func(c *Command) { c.Env["BAD=NAME"] = "bad" }} {
		c := good
		c.Env = map[string]string{}
		change(&c)
		if _, err := r.Run(t.Context(), c); err == nil {
			t.Fatal(c)
		}
	}
	r.quarantined.Store(true)
	if _, err := r.Run(t.Context(), good); err == nil {
		t.Fatal("admitted quarantined runner")
	}
}

func TestUnreapedChildQuarantines(t *testing.T) {
	r := testRunner(t, Config{KillGrace: time.Millisecond, SettleTimeout: 20 * time.Millisecond})
	owned := 0
	r.process.signal = func(pid int, _ syscall.Signal) error { owned = pid; return nil }
	r.process.table = func(context.Context) ([]byte, error) { return nil, nil }
	defer func() {
		if owned != 0 {
			_ = signalGroup(owned, syscall.SIGKILL)
		}
	}()
	c := helperTask(t, "sleep", "30s")
	c.Timeout = 100 * time.Millisecond
	got := runCommand(t, r, c)
	if got.Status != TimedOut || got.Settled {
		t.Fatal(got)
	}
	if _, err := r.Run(t.Context(), c); err == nil {
		t.Fatal("quarantine did not stick")
	}
}
