package check

import "testing"

func TestComputeScope(t *testing.T) {
	for _, c := range []struct {
		name, mb string
		paths    []string
		want     string
	}{
		{"no merge-base", "", []string{"a.go"}, ScopeFull},
		{"go file", "m", []string{"a.go", "docs/x.md"}, ScopeAffected},
		{"no paths", "m", nil, ScopeAffected},
		{"go.mod", "m", []string{"go.mod"}, ScopeFull},
		{"nested lock", "m", []string{"web/pnpm-lock.yaml"}, ScopeFull},
		{"bun.lockb", "m", []string{"bun.lockb"}, ScopeFull},
		{"verify.yaml", "m", []string{"verify.yaml"}, ScopeFull},
		{"workflow", "m", []string{".github/workflows/ci.yml"}, ScopeFull},
		{"prek yml", "m", []string{".pre-commit-config.yml"}, ScopeFull},
		{"custom golangci", "m", []string{".golangci-custom.toml"}, ScopeFull},
		{"npm shrinkwrap", "m", []string{"npm-shrinkwrap.json"}, ScopeFull},
		{"prek config", "m", []string{".pre-commit-config.yaml"}, ScopeFull},
		{"Makefile", "m", []string{"sub/Makefile"}, ScopeFull},
		{"mk", "m", []string{"rules.mk"}, ScopeFull},
		{"Taskfile", "m", []string{"Taskfile.yaml"}, ScopeFull},
		{"mise", "m", []string{"mise.toml"}, ScopeFull},
		{"justfile", "m", []string{"justfile"}, ScopeFull},
	} {
		got, reason := computeScope(c.mb, c.paths)
		if got != c.want || (got == ScopeFull) != (reason != "") {
			t.Errorf("%s: %q %q", c.name, got, reason)
		}
	}
}

func TestComputeScopeFixtureOnly(t *testing.T) {
	for _, c := range []struct {
		name       string
		paths      []string
		wantScope  string
		wantReason string
	}{
		{"testdata only", []string{"internal/x/testdata/in.json", "testdata/out.txt"}, ScopeFull, "fixture-only change"},
		{"nested fixtures dir", []string{"pkg/sub/fixtures/in.txt"}, ScopeFull, "fixture-only change"},
		{"__snapshots__", []string{"web/__snapshots__/a.snap"}, ScopeFull, "fixture-only change"},
		{"snapshots", []string{"web/snapshots/a.png"}, ScopeFull, "fixture-only change"},
		{"__fixtures__", []string{"web/__fixtures__/a.ts"}, ScopeFull, "fixture-only change"},
		{"mixed with code", []string{"a.go", "testdata/in.json"}, ScopeAffected, ""},
		{"file named like a fixture dir", []string{"testdata"}, ScopeAffected, ""},
		{"similar directory name", []string{"mytestdata/a.txt"}, ScopeAffected, ""},
		{"fixture plus lockfile keeps the lockfile reason", []string{"testdata/a.txt", "go.mod"}, ScopeFull, "manifest or lockfile changed: go.mod"},
	} {
		t.Run(c.name, func(t *testing.T) {
			scope, reason := computeScope("m", c.paths)
			if scope != c.wantScope || reason != c.wantReason {
				t.Fatalf("got %q %q, want %q %q", scope, reason, c.wantScope, c.wantReason)
			}
		})
	}
}
