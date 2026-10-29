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
		{"gate.yaml", "m", []string{"gate.yaml"}, ScopeFull},
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
