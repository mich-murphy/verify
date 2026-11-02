package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/config"
)

const goModFile = "module x\n\ngo 1.26.1\n"

const lefthook = `pre-commit:
  jobs:
    - name: format
      run: make fmt-check
    - name: lint
      run: make lint
    - name: tidy
      run: make tidy-check
    - name: actionlint
      run: make actionlint
pre-push:
  jobs:
    - name: test
      run: make test
`

// memoryShaped mirrors the repository that produced the uncached cover hook.
func memoryShaped() map[string]string {
	return map[string]string{
		"go.mod":       goModFile,
		"lefthook.yml": lefthook,
		"Makefile": "fmt-check:\n\ttrue\nlint:\n\ttrue\ntidy-check:\n\ttrue\n" +
			"cover:\n\tgo test -race -count=1 -shuffle=on -coverprofile=c.out ./...\n" +
			"deadcode:\n\ttrue\nvuln:\n\ttrue\nactionlint:\n\ttrue\n",
		".github/workflows/ci.yml": "on: push\n",
	}
}

const refusal = "make cover runs tests uncached (-count=1 or -shuffle); add a cacheable target such as `test-hook: go test -race ./...` and rerun verify setup --apply"

func report(t *testing.T, d string) audit.Report {
	t.Helper()
	rep, err := audit.Run(t.Context(), d, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestRefusesUncachedCoverHookAndPreviewEqualsWrittenFile(t *testing.T) {
	fakePrek(t)
	d := tmp(t, memoryShaped())
	rep := report(t, d)
	plan, err := Build(d, rep)
	if err != nil {
		t.Fatal(err)
	}
	res, written := apply(t, d)
	if plan.Text == "" || plan.Text != written {
		t.Fatalf("preview differs from written file:\n--- preview\n%s\n--- written\n%s", plan.Text, written)
	}
	if strings.Contains(written, "make cover") || strings.Contains(written, "id: coverage") {
		t.Fatalf("uncached hook written:\n%s", written)
	}
	if !strings.Contains(written, `"make fmt-check"`) {
		t.Fatalf("cacheable hooks must still be written:\n%s", written)
	}
	if len(res.Findings) != 1 || res.Findings[0].ID != "setup:coverage" || res.Findings[0].Severity != "block" || res.Findings[0].Message != refusal {
		t.Fatalf("findings = %+v", res.Findings)
	}
}

func TestWritesCacheableTestHookTarget(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{"go.mod": goModFile, "Makefile": "test-hook:\n\tgo test -race ./...\n"})
	res, written := apply(t, d)
	if !strings.Contains(written, "id: coverage\n") || !strings.Contains(written, `"make test-hook"`) || len(res.Findings) != 0 {
		t.Fatalf("res=%+v\n%s", res, written)
	}
}

func TestHookManagerNoteListsJobsWithoutCounterpart(t *testing.T) {
	fakePrek(t)
	d := tmp(t, memoryShaped())
	plan, err := Build(d, report(t, d))
	if err != nil {
		t.Fatal(err)
	}
	got := HookManagers(d, plan.Final())
	want := "lefthook.yml has jobs with no counterpart in the prek config (the file is left untouched): test (make test)"
	if len(got) != 1 || got[0].ID != "setup:hook-manager" || got[0].Severity != "note" || got[0].Message != want {
		t.Fatalf("got %+v, want message %q", got, want)
	}
	before, _ := os.ReadFile(filepath.Join(d, "lefthook.yml")) // #nosec G304 -- test temp dir
	apply(t, d)
	after, _ := os.ReadFile(filepath.Join(d, "lefthook.yml")) // #nosec G304 -- test temp dir
	if string(before) != string(after) {
		t.Fatal("apply modified lefthook.yml")
	}
}

func TestHookManagerKinds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			"husky",
			map[string]string{".husky/pre-commit": "#!/bin/sh\n. \"$(dirname \"$0\")/_/husky.sh\"\nnpx lint-staged\nnpm test\n"},
			".husky has jobs with no counterpart in the prek config (the file is left untouched): pre-commit (npx lint-staged), pre-commit (npm test)",
		},
		{
			"lint-staged",
			map[string]string{".lintstagedrc.json": `{"*.ts": ["oxlint", "oxfmt --check"]}`},
			".lintstagedrc.json has jobs with no counterpart in the prek config (the file is left untouched): *.ts (oxlint), *.ts (oxfmt --check)",
		},
		{
			"all covered",
			map[string]string{"lefthook.yml": "pre-commit:\n  commands:\n    lint:\n      run: make lint\n"},
			"lefthook.yml: every job has a counterpart in the prek config (the file is left untouched)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			final := "repos:\n  - repo: local\n    hooks:\n      - id: lint\n        entry: \"make  lint\"\n"
			got := HookManagers(tmp(t, tc.files), final)
			if len(got) != 1 || got[0].Message != tc.want {
				t.Fatalf("got %+v, want %q", got, tc.want)
			}
		})
	}
	if got := HookManagers(tmp(t, map[string]string{"go.mod": goModFile}), ""); len(got) != 0 {
		t.Fatalf("no hook manager, got %+v", got)
	}
}

func TestFallsBackToTestHookTarget(t *testing.T) {
	fakePrek(t)
	files := memoryShaped()
	files["Makefile"] += "test-hook:\n\tgo test -race ./...\n"
	d := tmp(t, files)
	res, written := apply(t, d)
	if !strings.Contains(written, `entry: "make test-hook"`) || strings.Contains(written, "make cover") {
		t.Fatalf("test-hook not used:\n%s", written)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("following the refusal message must clear it: %+v", res.Findings)
	}
}
