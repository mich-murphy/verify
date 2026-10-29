package verify

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/runner"
	"github.com/mich-murphy/verify/internal/state"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func put(t *testing.T, dir, name, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// repo creates main with the given verify yaml, then a feature commit touching touch.
func repo(t *testing.T, verifyYAML, touch string) *gitx.Repo {
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
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	git(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "verify.yaml", "base: main\n"+verifyYAML, 0o600)
	put(t, dir, "tool.sh", "#!/bin/sh\ncat\nexit 3\n", 0o755)
	put(t, dir, "cmd/x.go", "package x\n", 0o600)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "feature")
	put(t, dir, touch, "changed\n", 0o600)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "change")
	r, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRun(t *testing.T) {
	hdr := "verify:\n  surfaces: [\"cmd/**\"]\n  setup: [sh, -c, \"exit 0\"]\n  scenarios:\n"
	tests := []struct {
		name, yaml, touch, verdict, rule string
		steps                            int
	}{
		{"no surface", hdr + "    - {name: a, run: [./tool.sh], exit: 3}\n", "other.txt", NoSurface, "", 0},
		{"go with stdin equals", hdr + "    - {name: a, run: [./tool.sh], stdin: \"hi\\n\", exit: 3, stdout_equals: \"hi\\n\"}\n", "cmd/y.go", Go, "", 2},
		{"go contains", hdr + "    - {name: a, run: [./tool.sh], stdin: \"hello\", exit: 3, stdout_contains: ell}\n", "cmd/y.go", Go, "", 2},
		{"wrong exit", hdr + "    - {name: a, run: [./tool.sh], exit: 0}\n", "cmd/y.go", NoGo, "verify:scenario:a", 2},
		{"equals is exact", hdr + "    - {name: a, run: [./tool.sh], stdin: \"hi\\n\", exit: 3, stdout_equals: hi}\n", "cmd/y.go", NoGo, "verify:scenario:a", 2},
		{"setup scenario name", hdr + "    - {name: setup, run: [./tool.sh], exit: 3}\n", "cmd/y.go", Go, "", 2},
		{"inconclusive scenario name", hdr + "    - {name: inconclusive, run: [./tool.sh], exit: 0}\n", "cmd/y.go", NoGo, "verify:scenario:inconclusive", 2},
		{"missing binary", hdr + "    - {name: a, run: [.verify-bin/nope], exit: 0}\n", "cmd/y.go", Inconclusive, "verify:inconclusive", 1},
		{"command not found", hdr + "    - {name: a, run: [nonexistent-cmd-xyz], exit: 0}\n", "cmd/y.go", Inconclusive, "verify:inconclusive", 2},
		{"timeout", hdr + "    - {name: a, run: [sleep, \"5\"], exit: 0, timeout: 200ms}\n", "cmd/y.go", Inconclusive, "verify:inconclusive", 2},
		{"setup fails", strings.Replace(hdr, "exit 0", "exit 4", 1) + "    - {name: a, run: [./tool.sh], exit: 3}\n", "cmd/y.go", Inconclusive, "verify:inconclusive", 1},
		{"multiple inconclusive", hdr + "    - {name: a, run: [.verify-bin/missing-a], exit: 0}\n    - {name: b, run: [.verify-bin/missing-b], exit: 0}\n", "cmd/y.go", Inconclusive, "verify:inconclusive", 1},
		{"mixed no-go and multiple inconclusive", hdr + "    - {name: a, run: [./tool.sh], exit: 0}\n    - {name: b, run: [.verify-bin/missing-b], exit: 0}\n    - {name: c, run: [.verify-bin/missing-c], exit: 0}\n", "cmd/y.go", NoGo, "verify:scenario:a", 2},
		{"no-go wins over inconclusive", hdr + "    - {name: a, run: [./tool.sh], exit: 0}\n    - {name: b, run: [.verify-bin/nope], exit: 0}\n", "cmd/y.go", NoGo, "verify:scenario:a", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := repo(t, tc.yaml, tc.touch)
			rec, err := Run(t.Context(), r, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if rec.Verdict != tc.verdict {
				t.Fatalf("verdict %s, want %s: %+v", rec.Verdict, tc.verdict, rec.Findings)
			}
			ids := map[string]bool{}
			for _, f := range rec.Findings {
				if ids[f.ID] {
					t.Fatalf("duplicate finding ID: %+v", rec.Findings)
				}
				ids[f.ID] = true
			}
			if tc.name == "multiple inconclusive" && len(rec.Findings) != 2 {
				t.Fatalf("lost inconclusive findings: %+v", rec.Findings)
			}
			if len(rec.Steps) != tc.steps {
				t.Errorf("steps = %d, want %d", len(rec.Steps), tc.steps)
			}
			found := tc.rule == ""
			for _, f := range rec.Findings {
				found = found || (f.Rule == tc.rule && f.Severity == state.SeverityBlock && f.Blocking())
			}
			if !found || (tc.rule == "" && len(rec.Findings) != 0) {
				t.Errorf("findings %+v, want rule %q", rec.Findings, tc.rule)
			}
			saved, err := state.Open(r.CommonDir).LoadVerify(rec.SHA)
			if err != nil || saved.Verdict != tc.verdict {
				t.Errorf("saved record: %v %+v", err, saved)
			}
			if _, err := os.Stat(filepath.Join(state.Open(r.CommonDir).WorktreesDir(), "verify")); tc.steps > 0 && err != nil {
				t.Errorf("verify worktree not kept: %v", err)
			}
		})
	}
}

func TestRunErrors(t *testing.T) {
	r := repo(t, "", "x.txt")
	if _, err := Run(t.Context(), r, Options{Ref: "nonexistent-ref"}); err == nil {
		t.Error("want error for bad ref")
	}
	if rec, err := Run(t.Context(), r, Options{Ref: "HEAD"}); err != nil || rec.Verdict != NoSurface {
		t.Errorf("got %v %v", rec, err)
	}
}

func TestAssertionPresenceAndStartFailures(t *testing.T) {
	hdr := "verify:\n  surfaces: [\"cmd/**\"]\n  scenarios:\n"
	tests := []struct{ name, scenario, want string }{
		{"empty equality", `    - {name: a, run: [./tool.sh], stdin: hi, exit: 3, stdout_equals: ""}` + "\n", NoGo},
		{"real exit 127", `    - {name: a, run: [/bin/sh, -c, "exit 127"], exit: 127}` + "\n", Go},
		{"nonexistent expected 127", `    - {name: a, run: [nonexistent-verify-cmd-xyz], exit: 127}` + "\n", Inconclusive},
		{"truncated contains unknown", `    - {name: a, run: [/bin/sh, -c, "head -c 1048577 /dev/zero | tr '\\000' x"], exit: 0, stdout_contains: absent}` + "\n", Inconclusive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := repo(t, hdr+tc.scenario, "cmd/new.go")
			got, err := Run(t.Context(), r, Options{})
			if err != nil || got.Verdict != tc.want {
				t.Fatalf("%v %+v", err, got)
			}
		})
	}
}

func TestSetupAndScenarioTrackedMutationCannotPass(t *testing.T) {
	for _, setup := range []bool{true, false} {
		t.Run(fmt.Sprint(setup), func(t *testing.T) {
			yaml := "verify:\n  surfaces: [\"cmd/**\"]\n"
			command := "[/bin/sh, -c, \"echo changed > cmd/x.go\"]"
			if setup {
				yaml += "  setup: " + command + "\n  scenarios:\n    - {name: a, run: [./tool.sh], exit: 3}\n"
			} else {
				yaml += "  scenarios:\n    - {name: a, run: " + command + ", exit: 0}\n"
			}
			r := repo(t, yaml, "cmd/y.go")
			if _, err := Run(t.Context(), r, Options{}); err == nil || !strings.Contains(err.Error(), "tracked worktree/index differs") {
				t.Fatalf("mutation accepted: %v", err)
			}
		})
	}
}

func TestVerifyLogWriteFailureFailsClosed(t *testing.T) {
	r := repo(t, "verify:\n  surfaces: [\"cmd/**\"]\n  scenarios:\n    - {name: a, run: [./tool.sh], exit: 3}\n", "cmd/new.go")
	sha, err := r.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(r.CommonDir, "verify", "runs", sha, "logs")
	if err = os.MkdirAll(filepath.Dir(logs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(logs, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), r, Options{}); err == nil {
		t.Fatal("missing verify log accepted")
	}
}

func TestTruncatedOutputAssertionsDecidable(t *testing.T) {
	equal := "abc"
	contains := "ab"
	missing := "later"
	for _, tc := range []struct {
		sc      config.Scenario
		res     runner.Result
		bad     bool
		unknown bool
	}{
		{config.Scenario{StdoutEquals: &equal}, runner.Result{Stdout: runner.Evidence{Output: "abc", Truncated: true, Bytes: 4}}, true, false},
		{config.Scenario{StdoutEquals: &equal}, runner.Result{Stdout: runner.Evidence{Output: "abcd", Truncated: true, Bytes: 5}}, true, false},
		{config.Scenario{StdoutEquals: &equal}, runner.Result{Stdout: runner.Evidence{Output: "ab", Truncated: true, Bytes: 3}}, false, true},
		{config.Scenario{StdoutEquals: &equal}, runner.Result{Stdout: runner.Evidence{Output: "ab", Truncated: true, Bytes: 4}}, true, false},
		{config.Scenario{StdoutEquals: &equal}, runner.Result{Stdout: runner.Evidence{Output: "ax", Truncated: true, Bytes: 3}}, true, false},
		{config.Scenario{StdoutContains: &contains}, runner.Result{Stdout: runner.Evidence{Output: "abc", Truncated: true}}, false, false},
		{config.Scenario{StdoutContains: &missing}, runner.Result{Stdout: runner.Evidence{Output: "abc", Truncated: true}}, false, true},
	} {
		bad, unknown := assertions(tc.sc, tc.res)
		if (bad != "") != tc.bad || unknown != tc.unknown {
			t.Fatalf("assertions %+v: %q %v", tc, bad, unknown)
		}
	}
}

func TestVerifyMarkerSpansSetupAndScenariosAndClears(t *testing.T) {
	for _, tc := range []struct{ name, scenario, want string }{
		{"pass", "test -f \"$PWD.quarantine\"", Go},
		{"failed settled", "test -f \"$PWD.quarantine\"; exit 7", NoGo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			yaml := "verify:\n  surfaces: [\"cmd/**\"]\n  setup: [/bin/sh, -c, 'test -f \"$PWD.quarantine\"']\n  scenarios:\n    - {name: a, run: [/bin/sh, -c, '" + tc.scenario + "'], exit: 0}\n"
			r := repo(t, yaml, "cmd/new.go")
			rec, err := Run(t.Context(), r, Options{})
			if err != nil || rec.Verdict != tc.want {
				t.Fatalf("%v %+v", err, rec)
			}
			marker := gitx.WorktreeQuarantine(filepath.Join(state.Open(r.CommonDir).WorktreesDir(), "verify"))
			if _, err = os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatalf("settled verify retained marker: %v", err)
			}
		})
	}
}
