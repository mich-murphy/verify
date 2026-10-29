package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/state"
)

// No profile: the fixture has no go.mod or package.json, so the setup audit
// detects nothing and these tests exercise lanes and status only.
const baseYAML = "base: main\n"

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper with fixed executable
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	// #nosec G703 -- test helper; paths are fixed names under t.TempDir.
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- test helper; paths are fixed names under t.TempDir.
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setup creates a repo on main and a feature branch; prekMode is "fake" or "none".
func setup(t *testing.T, prekMode string) string {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	bin, _ := filepath.EvalSymlinks(t.TempDir())
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git missing")
	}
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	if prekMode == "fake" {
		write(t, bin, "prek", "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\nif [ -f fail-hook ]; then echo hook failed; exit 1; fi\necho ok\n")
		if err := os.Chmod(filepath.Join(bin, "prek"), 0o755); err != nil { //nolint:gosec // fake executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	gitRun(t, repo, "init", "-q", "-b", "main")
	write(t, repo, "gate.yaml", baseYAML)
	write(t, repo, ".pre-commit-config.yaml", "repos: []\n")
	write(t, repo, "a.go", "package a\n")
	write(t, repo, "README.md", "hi\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "feature")
	t.Chdir(repo)
	return repo
}

func commit(t *testing.T, repo, file, content string) {
	t.Helper()
	write(t, repo, file, content)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "change "+file)
}

func gate(t *testing.T, args ...string) (Result, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(append(args, "--json"), strings.NewReader(""), &out, &errb)
	var r Result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("bad json %q (stderr %q): %v", out.String(), errb.String(), err)
	}
	return r, code
}

func TestCheckPassAndStatus(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "a.go", "package a\n\nvar X = 1\n")
	r, code := gate(t, "check")
	if code != 0 || r.Status != "pass" || r.Lane != "standard" || len(r.Steps) != 2 || r.Steps[0].Name != "prek-pre-commit" {
		t.Fatalf("code=%d %+v", code, r)
	}
	if _, err := os.Stat(r.Steps[0].Log); err != nil {
		t.Fatalf("log missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "gate", "worktrees", "check")); err != nil {
		t.Fatalf("worktree not kept for reuse: %v", err)
	}
	s, code := gate(t, "status")
	if code != 0 || s.SHA != r.SHA || !strings.Contains(s.Message, "check:  pass") || s.NextAction != "run gate review" {
		t.Fatalf("code=%d %+v", code, s)
	}
}

func TestCheckFailingHook(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "fail-hook", "x\n")
	r, code := gate(t, "check")
	if code != 1 || r.Status != "fail" || r.NextAction == "" {
		t.Fatalf("code=%d %+v", code, r)
	}
	found := false
	for _, f := range r.Findings {
		found = found || (f.Rule == "check:prek-pre-commit" && f.Severity == "block")
	}
	if !found {
		t.Fatalf("no block finding: %+v", r.Findings)
	}
	s, _ := gate(t, "status")
	if s.Status != "blocked-by-findings" {
		t.Fatalf("%+v", s)
	}
}

func TestCheckMissingPrek(t *testing.T) {
	repo := setup(t, "none")
	commit(t, repo, "a.go", "package a\n\nvar Y = 1\n")
	r, code := gate(t, "check")
	if code != 1 || r.Steps[0].Status != "fail" {
		t.Fatalf("code=%d %+v", code, r)
	}
}

func TestDocsLane(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "README.md", "changed\n")
	r, code := gate(t, "check")
	if code != 0 || r.Lane != "docs" {
		t.Fatalf("code=%d %+v", code, r)
	}
	s, _ := gate(t, "status")
	if s.NextAction != "run gate ship" {
		t.Fatalf("%+v", s)
	}
}

func TestSignoffLaneWhenGateYAMLChanges(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "gate.yaml", baseYAML+"signoff_paths: [\"x/**\"]\n")
	r, code := gate(t, "check")
	if code != 0 || r.Lane != "signoff" {
		t.Fatalf("code=%d %+v", code, r)
	}
}

// Each command's own test file covers its behaviour; this test covers dispatch only.
func TestUsage(t *testing.T) {
	setup(t, "fake")
	for _, c := range []string{"check", "review", "verify", "ship", "decide", "status", "setup", "doctor", "agent-hook"} {
		if commands[c] == nil {
			t.Errorf("%s not registered", c)
		}
	}
	var o, e bytes.Buffer
	if Run([]string{"nope"}, nil, &o, &e) != ExitUsage {
		t.Fatal("unknown command should be usage error")
	}
	if Run([]string{"--help"}, nil, &o, &e) != 0 || !strings.Contains(o.String(), "usage") {
		t.Fatal("help")
	}
	for _, args := range [][]string{{"--json"}, {"nope", "--json"}, {"check", "--bad", "--json"}} {
		o.Reset()
		e.Reset()
		if code := Run(args, nil, &o, &e); code != ExitUsage {
			t.Errorf("%v: code %d", args, code)
		}
		var r Result
		if err := json.Unmarshal(o.Bytes(), &r); err != nil || r.Schema != ResultSchema || r.NextAction == "" || e.Len() != 0 {
			t.Errorf("%v: result %+v stderr %q error %v", args, r, e.String(), err)
		}
	}
	helpFlags := map[string]string{
		"check": "-fast", "review": "-reviewer", "verify": "-json", "ship": "-remote",
		"decide": "-reason", "status": "-json", "setup": "-apply", "doctor": "-json", "agent-hook": "-json",
	}
	o.Reset()
	e.Reset()
	if code := Run([]string{"nope", "--", "--json"}, nil, &o, &e); code != ExitUsage || strings.Contains(o.String(), `"schema"`) {
		t.Errorf("-- sentinel should stop JSON detection: code %d output %q", code, o.String())
	}
	for _, name := range []string{"check", "review", "verify", "ship", "decide", "status", "setup", "doctor", "agent-hook"} {
		o.Reset()
		e.Reset()
		if code := Run([]string{name, "--bogus", "--json"}, nil, &o, &e); code != ExitUsage {
			t.Errorf("%s: unknown flag code %d", name, code)
		}
		var r Result
		if err := json.Unmarshal(o.Bytes(), &r); err != nil || r.Command != name || r.Schema != ResultSchema || r.NextAction == "" || e.Len() != 0 {
			t.Errorf("%s: unknown flag result %+v stderr %q error %v", name, r, e.String(), err)
		}
		for _, args := range [][]string{{name, "--help"}, {name, "--help", "--json"}} {
			o.Reset()
			e.Reset()
			code := Run(args, nil, &o, &e)
			if code != ExitOK || !strings.Contains(o.String(), "usage: gate "+name) || !strings.Contains(o.String(), helpFlags[name]) {
				t.Errorf("%v: code %d output %q stderr %q", args, code, o.String(), e.String())
			}
			if len(args) == 3 {
				var result Result
				if err := json.Unmarshal(o.Bytes(), &result); err != nil || result.Status != "ok" || result.Command != name || e.Len() != 0 {
					t.Errorf("%v: result %+v stderr %q error %v", args, result, e.String(), err)
				}
			}
		}
	}
}

func TestDecideReasonValueIsNotHelp(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "README.md", "changed\n")
	r, code := gate(t, "decide", "approve", "--reason", "--help")
	if code != ExitOK || r.Status != "ok" {
		t.Fatalf("reason was interpreted as help: code %d result %+v", code, r)
	}
	branch, err := state.Open(filepath.Join(repo, ".git")).LoadBranch("feature")
	if err != nil || branch == nil || len(branch.Decisions) != 1 || branch.Decisions[0].Reason != "--help" {
		t.Fatalf("reason was not recorded: branch %+v error %v", branch, err)
	}
}

func TestStatusAndCheckErrorPaths(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "a.go", "package a\n\nvar Z = 1\n")
	var sink bytes.Buffer
	if code := Run([]string{"check", "--bogus"}, nil, &sink, &sink); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
	if code := Run([]string{"status", "extra"}, nil, &sink, &sink); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
	if r, code := gate(t, "check", "--sha", "nope"); code != ExitBlocked || r.NextAction == "" {
		t.Fatalf("code %d %+v", code, r)
	}
	if _, code := gate(t, "check"); code != 0 {
		t.Fatalf("code %d", code)
	}
	// corrupt record fails closed
	r, _ := gate(t, "status")
	p := filepath.Join(repo, ".git", "gate", "runs", r.SHA, "check.json")
	write(t, "/", p, "{bad")
	if s, code := gate(t, "status"); code != ExitBlocked || s.NextAction == "" {
		t.Fatalf("code %d %+v", code, s)
	}
}

func TestTextOutputAndNotRepo(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "fail-hook", "x\n")
	var out, errb bytes.Buffer
	code := Run([]string{"check"}, nil, &out, &errb)
	if code != 1 || !strings.Contains(out.String(), "gate check: fail") || !strings.Contains(out.String(), "next:") {
		t.Fatalf("code %d %q", code, out.String())
	}
	out.Reset()
	if Run([]string{"status"}, nil, &out, &errb) != 0 || !strings.Contains(out.String(), "review budget") {
		t.Fatalf("%q", out.String())
	}
	t.Chdir(t.TempDir())
	for _, c := range []string{"check", "status"} {
		if _, code := gate(t, c); code != ExitBlocked {
			t.Fatalf("%s code %d", c, code)
		}
	}
	if Run(nil, nil, &out, &errb) != ExitUsage {
		t.Fatal("no args")
	}
}
