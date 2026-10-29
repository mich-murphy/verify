package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOSBoundaryInspectionFailureQuarantines(t *testing.T) {
	r := testRunner(t, Config{SettleTimeout: 20 * time.Millisecond})
	r.process.table = func(context.Context) ([]byte, error) { return nil, errors.New("inspection unavailable") }
	got := runCommand(t, r, helperTask(t, "ok"))
	if got.Status != Fail || got.Settled || !strings.Contains(got.Error, "inspection unavailable") {
		t.Fatal(got)
	}
	if _, err := r.Run(t.Context(), helperTask(t, "ok")); err == nil {
		t.Fatal("quarantine lost")
	}
}

func TestSettlementSignalFailure(t *testing.T) {
	r := testRunner(t, Config{})
	sentinel := errors.New("signal rejected")
	r.process.signal = func(_ int, _ syscall.Signal) error { return sentinel }
	if err := r.settle(t.Context(), 42); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func TestConfigCopyAndLimits(t *testing.T) {
	for _, cfg := range []Config{{OutputLimit: -1}, {KillGrace: -1}, {SettleTimeout: -1}, {Env: map[string]string{"bad=name": "x"}}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted invalid config %+v", cfg)
		}
	}
	env := map[string]string{"APP": "old"}
	inherit := []string{"PATH"}
	r, err := New(Config{Env: env, Inherit: inherit})
	if err != nil {
		t.Fatal(err)
	}
	env["APP"] = "new"
	inherit[0] = "SEEDED_SECRET"
	c := helperTask(t, "env", "arg")
	got := runCommand(t, r, c)
	if got.Status != Pass || !strings.Contains(got.Stdout.Output, "APP=old;") || strings.Contains(got.Stdout.Output, "SECRET=") && !strings.Contains(got.Stdout.Output, "SECRET=;") {
		t.Fatal(got)
	}
}

func TestExecutableControlledPath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "fixture")
	// #nosec G306 -- Fixture command must be executable.
	if err := os.WriteFile(f, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".", "relative"} {
		if _, err := executable("fixture", dir, []string{"PATH=" + path}); err == nil {
			t.Fatal("relative PATH accepted", path)
		}
	}
	if resolved, err := executable("fixture", dir, []string{"PATH=" + dir}); err != nil || resolved != f {
		t.Fatalf("%q %v", resolved, err)
	}
}

func TestUnrelatedProcessSurvivesTimeout(t *testing.T) {
	c := helperTask(t, "sleep", "30s")
	// #nosec G204 -- Test-owned self-executable fixture with explicit argv.
	other := exec.CommandContext(t.Context(), c.Args[0], c.Args[1:]...)
	other.Env = []string{"RUNNER_HELPER=1", "GORACE=atexit_sleep_ms=0"}
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	c.Timeout = 50 * time.Millisecond
	got := runCommand(t, testRunner(t, Config{}), c)
	if got.Status != TimedOut || !got.Settled {
		t.Fatal(got)
	}
	if err := syscall.Kill(other.Process.Pid, 0); err != nil {
		t.Fatal("unrelated group killed", err)
	}
}

func TestBoundedGroupInspectionAndPolling(t *testing.T) {
	r := testRunner(t, Config{KillGrace: 20 * time.Millisecond, SettleTimeout: 100 * time.Millisecond})
	sentinel := errors.New("inspection rejected")
	r.process.table = func(context.Context) ([]byte, error) { return nil, sentinel }
	if err := r.settle(t.Context(), 42); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	queries := 0
	start := time.Now()
	r.process.signal = func(_ int, _ syscall.Signal) error { return nil }
	r.process.table = func(context.Context) ([]byte, error) {
		queries++
		if queries > 15 {
			return nil, errors.New("unbounded polling")
		}
		if time.Since(start) < 30*time.Millisecond {
			return []byte("42 S\n"), nil
		}
		return nil, nil
	}
	if err := r.settle(t.Context(), 42); err != nil || queries > 15 || time.Since(start) > time.Second {
		t.Fatalf("polling %d %v", queries, err)
	}
}

func TestMalformedGroupInspectionFailsClosed(t *testing.T) {
	table := func(context.Context) ([]byte, error) { return []byte("nonnumeric S\n"), nil }
	if alive, err := inspectGroup(t.Context(), 42, table); err == nil || alive {
		t.Fatalf("malformed process table: %v %v", alive, err)
	}
}

func TestRepositoryEnvDropsGitOverrides(t *testing.T) {
	t.Setenv("GIT_INDEX_FILE", "bad-index")
	t.Setenv("GATE_EXPECTED", "kept")
	env := RepositoryEnv()
	if _, ok := env["GIT_INDEX_FILE"]; ok || env["GATE_EXPECTED"] != "kept" {
		t.Fatal("inherited unsafe Git env", env["GIT_INDEX_FILE"])
	}
}
