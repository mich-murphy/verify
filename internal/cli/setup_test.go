package cli

import (
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
