package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type step struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type finding struct {
	Rule    string `json:"rule"`
	Hook    string `json:"hook"`
	Log     string `json:"log"`
	Excerpt string `json:"excerpt"`
	Message string `json:"message"`
}

type result struct {
	Command  string    `json:"command"`
	Lane     string    `json:"lane"`
	Status   string    `json:"status"`
	Steps    []step    `json:"steps"`
	Findings []finding `json:"findings"`
	Message  string    `json:"message"`
}

func (r result) rules() []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Rule)
	}
	return out
}

func (r result) finding(rule string) (finding, bool) {
	for _, f := range r.Findings {
		if f.Rule == rule {
			return f, true
		}
	}
	return finding{}, false
}

func (r result) stepNames() []string {
	var out []string
	for _, s := range r.Steps {
		out = append(out, s.Name)
	}
	return out
}

type fixture struct {
	t   *testing.T
	dir string
	bin string
}

// newFixture creates a repository on main holding files, then switches to a
// scenario branch. Its state directory is .git/verify, so it is fresh per call.
func newFixture(t *testing.T, files func(dir string) map[string]string) *fixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, dir: dir, bin: binary(t)}
	f.git("init", "-q", "-b", "main")
	f.git("config", "user.name", "t")
	f.git("config", "user.email", "t@example.com")
	for name, content := range files(dir) {
		f.write(name, content)
	}
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "base")
	f.git("checkout", "-q", "-b", "feature")
	return f
}

func (f *fixture) env() []string {
	e := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GOTOOLCHAIN=local",
	}
	return append(e, goEnv...)
}

func (f *fixture) git(args ...string) {
	f.t.Helper()
	c := exec.CommandContext(f.t.Context(), "git", args...) // #nosec G204 -- test helper with fixed executable
	c.Dir = f.dir
	c.Env = f.env()
	if out, err := c.CombinedOutput(); err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (f *fixture) write(name, content string) {
	f.t.Helper()
	p := filepath.Join(f.dir, name)
	// #nosec G703 -- test helper; names are fixed paths under a temp dir.
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		f.t.Fatal(err)
	}
	// #nosec G703 -- test helper; names are fixed paths under a temp dir.
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// commit writes one file and commits it.
func (f *fixture) commit(name, content string) {
	f.t.Helper()
	f.write(name, content)
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "change "+name)
}

// timedRun executes the built binary in the repository and returns stdout, the exit code and the wall time.
func (f *fixture) timedRun(args ...string) (string, int, time.Duration) {
	f.t.Helper()
	c := exec.CommandContext(f.t.Context(), f.bin, args...) // #nosec G204 -- binary built by this test
	c.Dir = f.dir
	c.Env = f.env()
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	start := time.Now()
	err := c.Run()
	wall := time.Since(start)
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		f.t.Fatalf("verify %v: %v", args, err)
	}
	f.t.Logf("verify %v exit %d wall %v\nstdout: %s\nstderr: %s", args, code, wall, stdout.String(), stderr.String())
	return stdout.String(), code, wall
}

// json runs a command with --json and decodes the result.
func (f *fixture) json(args ...string) (result, int) {
	r, code, _ := f.timedJSON(args...)
	return r, code
}

func (f *fixture) timedJSON(args ...string) (result, int, time.Duration) {
	f.t.Helper()
	out, code, wall := f.timedRun(append(args, "--json")...)
	var r result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		f.t.Fatalf("verify %v: bad json %q: %v", args, out, err)
	}
	return r, code, wall
}

func (f *fixture) marker() string { return filepath.Join(f.dir, ".git", "hook-ran") }

func (f *fixture) hookRan() bool {
	_, err := os.Stat(f.marker())
	return err == nil
}
