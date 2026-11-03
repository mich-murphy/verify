package e2e

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// hook writes a marker file before running cmd so a test can tell whether prek ran it.
func hook(dir, id, stage, cmd, types string) string {
	return fmt.Sprintf(`      - id: %[1]s
        name: %[1]s
        entry: sh -c 'echo ran >> %[2]s/.git/hook-ran; %[4]s'
        language: system
        pass_filenames: false
        %[5]s
        stages: [%[3]s]
`, id, dir, stage, cmd, types)
}

func prekConfig(hooks ...string) string {
	s := "repos:\n  - repo: local\n    hooks:\n"
	for _, h := range hooks {
		s += h
	}
	return s
}

const goTestCmd = "go test ./... && false"

func goFixture(dir string) map[string]string {
	return map[string]string{
		"go.mod":            "module fixture\n\ngo 1.27\n",
		"calc/calc.go":      "package calc\n\nfunc Add(a, b int) int { return a + b }\n",
		"calc/calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n}\n",
		"README.md":         "fixture\n",
		"verify.yaml":       waivers("format", "lint", "tidy", "coverage", "deadcode", "vuln", "golangci-config"),
		".pre-commit-config.yaml": prekConfig(
			hook(dir, "vet", "pre-commit", "go vet ./...", "types: [go]"),
			hook(dir, "test", "pre-push", goTestCmd, "types: [go]"),
		),
	}
}

const addFile = "package calc\n\nfunc Add(a, b int) int { return a + b }\n"

func TestGoSetupAudit(t *testing.T) {
	f := newFixture(t, goFixture)
	r, code := f.json("setup")
	if code != 0 || r.Status != "pass" || !strings.Contains(r.Message, "test: ok (hook test)") {
		t.Fatalf("code=%d %+v", code, r)
	}
	if contains(r.rules(), "setup:test") || contains(r.rules(), "setup:format") {
		t.Fatalf("unexpected findings %v", r.rules())
	}
}

func TestGoCleanSourceChange(t *testing.T) {
	f := newFixture(t, goFixture)
	f.commit("calc/calc.go", addFile+"\nfunc Sub(a, b int) int { return a - b }\n")
	r, code := f.json("check")
	if code != 0 || r.Status != "pass" || r.Lane != "standard" {
		t.Fatalf("code=%d %+v", code, r)
	}
	if got := r.stepNames(); !slices.Equal(got, []string{"prek-pre-commit", "prek-pre-push"}) {
		t.Fatalf("steps %v", got)
	}
	s, code := f.json("status")
	if code != 3 || s.Status != "ok" {
		t.Fatalf("status code=%d %+v", code, s)
	}
}

func TestGoFailingTestCommit(t *testing.T) {
	f := newFixture(t, goFixture)
	f.commit("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"boom\") }\n")
	r, code := f.json("check")
	if code != 1 || r.Status != "fail" {
		t.Fatalf("code=%d %+v", code, r)
	}
	fd, ok := r.finding("check:pre-push:test")
	if !ok || fd.Hook != "test" || !strings.Contains(fd.Excerpt, "boom") || fd.Log == "" {
		t.Fatalf("finding %+v in %v", fd, r.rules())
	}
	s, code := f.json("status")
	if code != 1 || s.Status != "blocked-by-findings" {
		t.Fatalf("status code=%d %+v", code, s)
	}
}

func TestGoDocsOnlyCommit(t *testing.T) {
	f := newFixture(t, goFixture)
	f.commit("README.md", "fixture\n\nmore docs\n")
	r, code := f.json("check")
	if code != 0 || r.Lane != "docs" {
		t.Fatalf("code=%d %+v", code, r)
	}
	if f.hookRan() {
		t.Fatalf("prek hooks ran on a docs-only commit: steps %v", r.stepNames())
	}
}

func TestGoSecretCommit(t *testing.T) {
	f := newFixture(t, goFixture)
	f.commit("cfg.txt", "# c\ntoken = "+"gh"+"p_"+"aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5"+"\n")
	r, code, wall := f.timedJSON("check")
	if code != 1 || wall > 5*time.Second {
		t.Fatalf("code=%d wall=%v %+v", code, wall, r)
	}
	if !slices.ContainsFunc(r.rules(), func(s string) bool { return strings.HasPrefix(s, "guard:secrets") }) {
		t.Fatalf("rules %v", r.rules())
	}
	if f.hookRan() {
		t.Fatal("prek hooks ran after a secret was found")
	}
}

func TestGoReuse(t *testing.T) {
	f := newFixture(t, goFixture)
	f.commit("calc/calc.go", addFile+"\nfunc Sub(a, b int) int { return a - b }\n")
	if r, code := f.json("check"); code != 0 {
		t.Fatalf("first check code=%d %+v", code, r)
	}
	r, code, wall := f.timedJSON("check")
	if code != 0 || !strings.Contains(r.Message, "already passed") || wall > 2*time.Second {
		t.Fatalf("same sha code=%d wall=%v %+v", code, wall, r)
	}
	for range 2 {
		f.git("commit", "-q", "--allow-empty", "-m", "same tree")
		r, code, wall = f.timedJSON("check")
		if code != 0 || !strings.Contains(r.Message, "reused check from") || wall > 2*time.Second {
			t.Fatalf("empty commit code=%d wall=%v %+v", code, wall, r)
		}
	}
}

func TestGoShipDryRun(t *testing.T) {
	f := newFixture(t, goFixture)
	f.commit("README.md", "fixture\n\nmore docs\n")
	if _, code := f.json("check"); code != 0 {
		t.Fatalf("check code=%d", code)
	}
	r, code := f.json("ship", "--dry-run")
	plan := strings.Index(r.Message, "push planned: git push --force-with-lease=refs/heads/feature")
	remote := strings.Index(r.Message, "remote failed: selected remote has no unique GitHub push URL")
	if code != 3 || r.Status != "blocked" || r.Lane != "docs" || plan < 0 || remote < plan {
		t.Fatalf("code=%d plan=%d remote=%d %+v", code, plan, remote, r)
	}
	if got := r.stepNames(); !slices.Equal(got, []string{"ship:push", "ship:pr", "ship:status", "ship:remote"}) {
		t.Fatalf("steps %v", got)
	}
}

func waivers(ids ...string) string {
	s := "base: main\nwaivers:\n"
	for _, id := range ids {
		s += "  - obligation: " + id + "\n    reason: fixture does not carry this tool\n"
	}
	return s
}
