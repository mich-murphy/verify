package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func shellTask(t *testing.T, script string) Command {
	t.Helper()
	return Command{Args: []string{"/bin/sh", "-c", script}, Dir: t.TempDir(), Timeout: 10 * time.Second}
}

// A command that exits 0 and whose owned descendants finish inside the settle
// window passes. Tools such as bun and turbo leave short-lived helpers behind.
func TestExitZeroWithShortLivedDescendantPasses(t *testing.T) {
	cases := map[string]string{
		"quiet":          "/bin/sleep 0.4 >/dev/null 2>&1 & exit 0",
		"holds pipe":     "/bin/sleep 0.4 & exit 0",
		"setsid escaped": "/usr/bin/perl -MPOSIX -e 'POSIX::setsid(); exec \"/bin/sleep\", \"0.4\"' >/dev/null 2>&1 & exit 0",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			got := runCommand(t, testRunner(t, Config{}), shellTask(t, script))
			if got.Status != Pass || got.ExitCode != 0 || !got.Settled || got.Error != "" {
				t.Fatalf("%+v", got)
			}
		})
	}
}

// A descendant still running after the settle window is a lifecycle failure,
// and the runner terminates it.
func TestExitZeroWithLongLivedDescendantFailsAndIsStopped(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	script := "/bin/sleep 30 >/dev/null 2>&1 & echo $! >" + pidFile + "; exit 0"
	got := runCommand(t, testRunner(t, Config{KillGrace: 20 * time.Millisecond}), shellTask(t, script))
	if got.Status != Fail || !got.Settled || got.Error != "command exited before owned descendants settled" {
		t.Fatalf("%+v", got)
	}
	// #nosec G304 -- PID file created inside the test temporary directory.
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(string(data))
	if _, err := strconv.Atoi(pid); err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- Fixed process listing with a parsed numeric PID only.
	ps, err := exec.CommandContext(t.Context(), "/bin/ps", "-p", pid, "-o", "stat=").Output()
	if err == nil && !strings.HasPrefix(strings.TrimSpace(string(ps)), "Z") {
		t.Fatalf("descendant survived: %s", ps)
	}
}
