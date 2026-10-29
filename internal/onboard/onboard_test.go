package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/config"
)

func fakePrek(t *testing.T) {
	t.Helper()
	bin, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte("#!/bin/sh\necho []\n"), 0o700); err != nil { //nolint:gosec // fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func tmp(t *testing.T, files map[string]string) string {
	t.Helper()
	d, _ := filepath.EvalSymlinks(t.TempDir())
	for n, c := range files {
		p := filepath.Join(d, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func apply(t *testing.T, d string) (Result, string) {
	t.Helper()
	rep, err := audit.Run(t.Context(), d, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(t.Context(), d, rep)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(d, prekFile)) // #nosec G304 -- test temp dir
	return res, string(b)
}

func TestApplyGoMakeTargets(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{
		"go.mod":   "module x\n\ngo 1.26.1\n",
		"Makefile": "fmt-check:\n\ttrue\ncover:\n\ttrue\nvuln:\n\ttrue\n",
	})
	_, p := apply(t, d)
	for _, want := range []string{`"make fmt-check"`, `"make cover"`, `"make vuln"`, "id: coverage", "types: [go]", "repo: builtin", "exclude: (^|/)testdata/", "priority: 10"} {
		if !strings.Contains(p, want) {
			t.Fatalf("missing %q in\n%s", want, p)
		}
	}
	if strings.Contains(p, "id: test\n") || strings.Contains(p, "- {") {
		t.Fatalf("unexpected:\n%s", p)
	}
}

// A fixer target (`fmt`, `tidy`) must never become a hook: hooks check, not fix.
func TestApplyIgnoresFixerTargets(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{
		"go.mod":   "module x\n\ngo 1.26.1\n",
		"Makefile": "fmt:\n\ttrue\ntidy:\n\ttrue\n",
	})
	_, p := apply(t, d)
	if strings.Contains(p, `"make fmt"`) || strings.Contains(p, `"make tidy"`) {
		t.Fatalf("fixer target used as a hook:\n%s", p)
	}
	for _, want := range []string{`"golangci-lint fmt --diff"`, `"go mod tidy -diff"`} {
		if !strings.Contains(p, want) {
			t.Fatalf("missing %q in\n%s", want, p)
		}
	}
}

func TestApplyAppendsAndIsIdempotent(t *testing.T) {
	fakePrek(t)
	orig := "# keep\nrepos:\n  - repo: local\n    hooks:\n      - id: x\n        name: x\n        entry: x\n        language: system\n"
	d := tmp(t, map[string]string{"go.mod": "module x\n\ngo 1.26.1\n", prekFile: orig})
	_, p := apply(t, d)
	if !strings.HasPrefix(p, orig) || !strings.Contains(p, "id: coverage") {
		t.Fatal(p)
	}
}

func TestApplyExistingConfigVariants(t *testing.T) {
	fakePrek(t)
	for _, name := range []string{".pre-commit-config.yml", "prek.toml"} {
		t.Run(name, func(t *testing.T) {
			orig := "# preserve\nrepos:\n  - repo: local\n    hooks:\n      - id: custom\n        name: custom\n        entry: true\n        language: system\n"
			if name == "prek.toml" {
				orig = "# preserve\n[[repos]]\nrepo = 'local'\n"
			}
			d := tmp(t, map[string]string{"go.mod": "module x\n\ngo 1.26.1\n", name: orig})
			res, _ := apply(t, d)
			if _, err := os.Stat(filepath.Join(d, prekFile)); !os.IsNotExist(err) {
				t.Fatalf("competing config created: %v", err)
			}
			content, err := os.ReadFile(filepath.Join(d, name)) // #nosec G304 -- fixed test config filename under t.TempDir
			if err != nil || !strings.HasPrefix(string(content), orig) {
				t.Fatalf("config changed unexpectedly: %s %v", content, err)
			}
			if name == "prek.toml" && !strings.Contains(strings.Join(res.Notes, " "), "by hand") {
				t.Fatalf("missing manual guidance: %+v", res)
			}
			if name != "prek.toml" && !strings.Contains(string(content), "id: coverage") {
				t.Fatalf("yml not extended: %s", content)
			}
		})
	}
}

func TestMixedStarterRequiresSharedEntryPoint(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{"go.mod": "module x\n\ngo 1.26.1\n", "package.json": `{}`, "package-lock.json": `{}`})
	res, text := apply(t, d)
	for _, id := range []string{"format", "lint", "test", "coverage"} {
		if strings.Contains(text, "id: "+id+"\n") {
			t.Fatalf("guessed %s in mixed project: %s", id, text)
		}
	}
	for _, id := range []string{"format", "lint", "coverage"} {
		if !strings.Contains(strings.Join(res.Notes, " "), "no "+id+" hook written for mixed profile") {
			t.Fatalf("missing %s guidance: %+v", id, res)
		}
	}
	if !strings.Contains(text, "id: tidy") {
		t.Fatalf("Go-only obligation missing: %s", text)
	}
}

func TestDeadcodeStarterPreservesToolFailure(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "deadcode"), []byte("#!/bin/sh\nexit 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(bin, "deadcode"), 0o700); err != nil { // #nosec G302 -- fake executable in test
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh", "-c", goDefs["deadcode"].starter) // #nosec G204 -- fixed starter command, controlled fake tool
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if err := cmd.Run(); err == nil {
		t.Fatal("failed deadcode with empty stdout must fail")
	}
}

func TestApplyNestedTypeScript(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{
		"web/bun.lock": "", "web/tsconfig.json": "{}",
		"web/package.json": `{"scripts":{"check-types":"x","test":"y","lint":"oxlint"},"devDependencies":{"oxlint":"1.0.0"}}`,
	})
	res, p := apply(t, d)
	for _, want := range []string{`"bun run --cwd web check-types"`, `"bun run --cwd web lint"`, `"bun run --cwd web test"`} {
		if !strings.Contains(p, want) {
			t.Fatalf("missing %s in\n%s", want, p)
		}
	}
	n := strings.Join(res.Notes, "\n")
	if !strings.Contains(n, "oxfmt is not a declared dependency") || strings.Contains(n, "prepare:") {
		t.Fatal(n)
	}
}
