package audit

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/state"
)

const (
	goMod  = "module x\n\ngo 1.26.1\n"
	tsRepo = `{"scripts":{}}`
)

func hookYAML(hooks ...string) string {
	return "repos:\n  - repo: local\n    hooks:\n" + strings.Join(hooks, "")
}

func tsFiles(cfg string) map[string]string {
	return map[string]string{
		"package.json": tsRepo, "package-lock.json": "{}", "tsconfig.json": `{}`,
		".pre-commit-config.yaml": cfg,
	}
}

// fakeTools puts executable stubs for names on PATH.
func fakeTools(t *testing.T, names ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, n := range names {
		// #nosec G306 -- test stub needs owner execute permission under t.TempDir.
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const piConfig = `minimum_prek_version: "0.5.0"
repos:
  - repo: builtin
    hooks:
      - id: trailing-whitespace
        stages: [pre-commit]
  - repo: local
    hooks:
      - id: format-check
        name: format-check
        entry: "npx oxfmt --check"
        language: system
        types_or: [javascript, jsx, ts, tsx, json]
        priority: 10
        stages: [pre-commit]
      - id: lint
        name: lint
        entry: "npm run lint"
        language: system
        types_or: [javascript, jsx, ts, tsx]
        pass_filenames: false
        priority: 10
        stages: [pre-commit]
      - id: typecheck
        name: typecheck
        entry: "npm run typecheck"
        language: system
        pass_filenames: false
        always_run: true
        stages: [pre-push]
      - id: test
        name: test
        entry: "npm run test:coverage -- --no-file-parallelism"
        language: system
        pass_filenames: false
        always_run: true
        stages: [pre-push]
`

func TestHookNotes(t *testing.T) {
	if _, err := exec.LookPath("prek"); err != nil {
		t.Skip("prek missing")
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  map[string]string // note id -> message
	}{
		{
			name: "scope: javascript-only types and a web/*.js path on a typescript project",
			files: tsFiles(hookYAML(
				"      - {id: lint, name: lint, entry: npm run lint, language: system, types_or: [javascript], stages: [pre-commit]}\n",
				"      - {id: format, name: format, entry: npm run fmt, language: system, files: '^web/.*\\.js$', stages: [pre-commit]}\n",
				"      - {id: test, name: test, entry: npm test, language: system, stages: [pre-push]}\n",
			)),
			want: map[string]string{
				"setup:lint:scope":   "hook lint: its files/types filters cannot match any typescript file, so it never checks this project",
				"setup:format:scope": "hook format: its files/types filters cannot match any typescript file, so it never checks this project",
			},
		},
		{
			name: "scope: a files filter on a subdirectory that holds tracked typescript",
			files: map[string]string{
				"package.json": tsRepo, "package-lock.json": "{}", "tsconfig.json": `{}`,
				"tools/ci/src/index.ts": "export {}\n",
				".pre-commit-config.yaml": hookYAML(
					"      - {id: lint, name: lint, entry: npm run lint, language: system, files: '^tools/ci/', types_or: [ts], stages: [pre-commit]}\n",
					"      - {id: format, name: format, entry: npm run fmt, language: system, files: '^nothing/', stages: [pre-commit]}\n",
				),
			},
			want: map[string]string{
				"setup:format:scope": "hook format: its files/types filters cannot match any typescript file, so it never checks this project",
			},
		},
		{
			name: "always-run on a pre-push hook",
			files: tsFiles(hookYAML(
				"      - {id: typecheck, name: t, entry: npm run typecheck, language: system, always_run: true, stages: [pre-push]}\n",
			)),
			want: map[string]string{
				"setup:typecheck:always-run": "hook typecheck is always_run at pre-push: docs-only commits will pay for it",
			},
		},
		{
			name: "uncacheable through a make recipe",
			files: map[string]string{
				"go.mod":   goMod,
				"Makefile": "cover:\n\tgo test -count=1 -shuffle=on -race ./...\n",
				".pre-commit-config.yaml": hookYAML(
					"      - {id: test, name: test, entry: make cover, language: system, stages: [pre-push]}\n"),
			},
			want: map[string]string{
				"setup:test:uncacheable": "hook test: `make cover` runs Go tests uncached (-count=1 or -shuffle), so prek cannot reuse results",
			},
		},
		{
			name: "uncacheable through a direct go test entry",
			files: map[string]string{
				"go.mod": goMod,
				".pre-commit-config.yaml": hookYAML(
					"      - {id: coverage, name: c, entry: 'go test -count=1 ./...', language: system, stages: [pre-push]}\n"),
			},
			want: map[string]string{
				"setup:coverage:uncacheable": "hook coverage: `go test -count=1 ./...` runs Go tests uncached (-count=1 or -shuffle), so prek cannot reuse results",
			},
		},
		{
			name: "a cacheable make target and a direct entry produce no notes",
			files: map[string]string{
				"go.mod":   goMod,
				"Makefile": "test-hook:\n\tgo test -race ./...\n",
				".pre-commit-config.yaml": hookYAML(
					"      - {id: test, name: test, entry: make test-hook, language: system, stages: [pre-push]}\n",
					"      - {id: coverage, name: c, entry: 'go test -count=10 ./...', language: system, stages: [pre-push]}\n"),
			},
		},
		{
			name: "tool missing",
			files: tsFiles(hookYAML(
				"      - {id: lint, name: lint, entry: nosuchtool-verify --check, language: system, stages: [pre-commit]}\n",
				"      - {id: format, name: format, entry: make fmt, language: system, stages: [pre-commit]}\n",
				"      - {id: typecheck, name: t, entry: true, language: system, stages: [pre-push]}\n",
			)),
			want: map[string]string{
				"setup:lint:tool-missing": "hook lint: nosuchtool-verify is not on PATH",
			},
		},
		{
			name:  "pi-extensions shape yields only the two always-run notes",
			files: tsFiles(piConfig),
			want: map[string]string{
				"setup:typecheck:always-run": "hook typecheck is always_run at pre-push: docs-only commits will pay for it",
				"setup:test:always-run":      "hook test is always_run at pre-push: docs-only commits will pay for it",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeTools(t, "npm", "npx", "golangci-lint")
			d := repo(t, tc.files)
			r, err := Run(t.Context(), d, config.Default())
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, f := range r.Findings() {
				if f.Severity == state.SeverityNote && strings.Count(f.ID, ":") == 2 {
					got[f.ID] = f.Message
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("notes = %v, want %v", got, tc.want)
			}
			for id, msg := range tc.want {
				if got[id] != msg {
					t.Errorf("%s = %q, want %q", id, got[id], msg)
				}
			}
			if slices.ContainsFunc(r.Findings(), func(f state.Finding) bool { return strings.Count(f.ID, ":") == 2 && f.Severity != state.SeverityNote }) {
				t.Fatal("hook notes must never block")
			}
		})
	}
}
