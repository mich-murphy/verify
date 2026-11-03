package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupReportsGapsAndWaivers(t *testing.T) {
	repo := setup(t, "none")
	write(t, repo, "go.mod", "module x\n\ngo 1.26.1\n")
	r, code := runCLI(t, "setup")
	if code != 1 || r.Status != "fail" || len(r.Findings) == 0 || !strings.Contains(r.Message, "missing") {
		t.Fatalf("code=%d %+v", code, r)
	}
	var ids []string
	for _, f := range r.Findings {
		if f.Severity == "block" {
			ids = append(ids, f.Rule[len("setup:"):])
		}
	}
	y := baseYAML + "waivers:\n"
	for _, id := range ids {
		y += "  - {obligation: " + id + ", reason: test}\n"
	}
	write(t, repo, "verify.yaml", y)
	r, code = runCLI(t, "setup")
	if code != 0 || r.Status != "pass" || !strings.Contains(r.Message, "waived") {
		t.Fatalf("code=%d %+v", code, r)
	}
}

func TestSetupNoProfileAndBadConfig(t *testing.T) {
	repo := setup(t, "none")
	write(t, repo, "verify.yaml", "")
	r, code := runCLI(t, "setup")
	if code != ExitFindings || !strings.Contains(r.Message, "profile: unsupported") || !strings.Contains(r.Message, "prek-config") {
		t.Fatalf("code=%d %+v", code, r)
	}
	write(t, repo, "verify.yaml", "bogus: 1\n")
	if _, code = runCLI(t, "setup"); code != ExitUsage {
		t.Fatal(code)
	}
}

func TestSetupApplyWritesAndReruns(t *testing.T) {
	repo := setup(t, "fake")
	write(t, repo, "go.mod", "module x\n\ngo 1.26.1\n")
	_ = os.Remove(filepath.Join(repo, "verify.yaml"))
	_ = os.Remove(filepath.Join(repo, ".pre-commit-config.yaml"))
	r, code := runCLI(t, "setup", "--apply")
	if code != 1 || !strings.Contains(r.Message, "Git hook shims") {
		t.Fatalf("code=%d %+v", code, r)
	}
	if _, err := os.Stat(filepath.Join(repo, ".pre-commit-config.yaml")); err != nil {
		t.Fatal(err)
	}
}

const golangciV2 = `version: "2"
linters:
  enable: [govet, staticcheck, errcheck, errorlint, ineffassign, unused, gosec, revive, gocritic, nolintlint, gocognit]
  settings:
    nolintlint:
      require-explanation: true
`

// memoryRepo is a Go repository whose hooks live in lefthook and whose cover
// target runs tests uncached.
func memoryRepo(t *testing.T) string {
	t.Helper()
	repo := setup(t, "fake")
	_ = os.Remove(filepath.Join(repo, "verify.yaml"))
	_ = os.Remove(filepath.Join(repo, ".pre-commit-config.yaml"))
	write(t, repo, "go.mod", "module x\n\ngo 1.26.1\n")
	write(t, repo, ".golangci.yml", golangciV2)
	write(t, repo, "lefthook.yml", "pre-commit:\n  jobs:\n    - name: lint\n      run: make lint\npre-push:\n  jobs:\n    - name: test\n      run: make test\n")
	write(t, repo, "Makefile", "lint:\n\ttrue\ncover:\n\tgo test -race -count=1 -shuffle=on ./...\n")
	return repo
}

func blockingRules(r Result) []string {
	var out []string
	for _, f := range r.Findings {
		if f.Severity == "block" {
			out = append(out, f.Rule)
		}
	}
	return out
}

func TestSetupWithoutPrekConfigReportsOneFindingAndPreviewsTheFile(t *testing.T) {
	repo := memoryRepo(t)
	r, code := runCLI(t, "setup")
	if got := blockingRules(r); code != ExitFindings || len(got) != 1 || got[0] != "setup:prek-config" {
		t.Fatalf("code=%d blocking=%v", code, got)
	}
	var out, errb bytes.Buffer
	Run([]string{"setup"}, strings.NewReader(""), &out, &errb)
	_, tail, ok := strings.Cut(out.String(), "would write .pre-commit-config.yaml:\n")
	if !ok {
		t.Fatalf("no preview in:\n%s", out.String())
	}
	preview, _, _ := strings.Cut(tail, "\nnext: ")
	if _, err := os.Stat(filepath.Join(repo, ".pre-commit-config.yaml")); err == nil {
		t.Fatal("preview wrote the config")
	}
	applied, code := runCLI(t, "setup", "--apply")
	written, err := os.ReadFile(filepath.Join(repo, ".pre-commit-config.yaml")) // #nosec G304 -- test temp dir
	if err != nil || strings.TrimSpace(preview) != strings.TrimSpace(string(written)) {
		t.Fatalf("code=%d err=%v\n--- preview\n%s\n--- written\n%s", code, err, preview, written)
	}
	var sawCover, sawManager bool
	for _, f := range applied.Findings {
		sawCover = sawCover || f.Rule == "setup:coverage" && strings.HasPrefix(f.Message, "make cover runs tests uncached (-count=1 or -shuffle); add a cacheable target such as `test-hook: go test -race ./...` and rerun verify setup --apply")
		sawManager = sawManager || f.Rule == "setup:hook-manager" && strings.Contains(f.Message, "lefthook.yml") && strings.Contains(f.Message, "test (make test)")
	}
	if !sawCover || !sawManager {
		t.Fatalf("cover refusal=%v hook-manager note=%v in %+v", sawCover, sawManager, applied.Findings)
	}
	lh, _ := os.ReadFile(filepath.Join(repo, "lefthook.yml")) // #nosec G304 -- test temp dir
	if !strings.Contains(string(lh), "make test") {
		t.Fatal("lefthook.yml changed")
	}
}

func TestSetupPreviewTextExplainsTheRefusedUncachedTarget(t *testing.T) {
	memoryRepo(t)
	var out, errb bytes.Buffer
	Run([]string{"setup"}, strings.NewReader(""), &out, &errb)
	const want = "make cover runs tests uncached (-count=1 or -shuffle); add a cacheable target such as `test-hook: go test -race ./...` and rerun verify setup --apply"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("text output lacks the refusal:\n%s", out.String())
	}
}

func TestSetupTestFindingCarriesTheRefusalWhenCoverIsTheOnlyCandidate(t *testing.T) {
	memoryRepo(t)
	r, _ := runCLI(t, "setup", "--apply")
	const want = "make cover runs tests uncached (-count=1 or -shuffle); add a cacheable target such as `test-hook: go test -race ./...` and rerun verify setup --apply"
	var got []string
	for _, f := range r.Findings {
		if f.Rule == "setup:test" {
			got = append(got, f.Message)
		}
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("setup:test findings %q, want one saying %q", got, want)
	}
}
