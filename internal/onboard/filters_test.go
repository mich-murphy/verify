package onboard

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type writtenHook struct {
	ID      string   `yaml:"id"`
	Files   string   `yaml:"files"`
	Types   []string `yaml:"types"`
	TypesOr []string `yaml:"types_or"`
}

func writtenHooks(t *testing.T, text string) map[string]writtenHook {
	t.Helper()
	var cfg struct {
		Repos []struct {
			Hooks []writtenHook `yaml:"hooks"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string]writtenHook{}
	for _, r := range cfg.Repos {
		for _, h := range r.Hooks {
			out[h.ID] = h
		}
	}
	return out
}

// selects follows prek: files, types and types_or must all admit the path.
func (h writtenHook) selects(p string) bool {
	tags := []string{"file", "text"}
	switch path.Ext(p) {
	case ".go":
		tags = append(tags, "go")
	case ".json":
		tags = append(tags, "json")
	case ".ts":
		tags = append(tags, "ts")
	}
	if h.Files != "" && !regexp.MustCompile(h.Files).MatchString(p) {
		return false
	}
	for _, ty := range h.Types {
		if !slices.Contains(tags, ty) {
			return false
		}
	}
	return len(h.TypesOr) == 0 || slices.ContainsFunc(h.TypesOr, func(ty string) bool { return slices.Contains(tags, ty) })
}

func TestGoProfileHooksSelectDependencyAndLinterConfigChanges(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{
		"go.mod": goModFile, "go.sum": "",
	})
	_, written := apply(t, d)
	hooks := writtenHooks(t, written)
	for _, tc := range []struct {
		hook, path string
		want       bool
	}{
		{"coverage", "go.sum", true},
		{"coverage", "go.mod", true},
		{"coverage", "sub/go.mod", true},
		{"coverage", "main.go", true},
		{"coverage", "README.md", false},
		{"vuln", "go.sum", true},
		{"vuln", "go.mod", true},
		{"vuln", "README.md", false},
		{"deadcode", "go.mod", true},
		{"deadcode", "pkg/x.go", true},
		{"tidy", "go.mod", true},
		{"tidy", "go.sum", true},
		{"tidy", "pkg/x.go", true},
		{"tidy", "README.md", false},
		{"lint", ".golangci.yml", true},
		{"lint", ".golangci.toml", true},
		{"lint", ".golangci-lint-version", true},
		{"lint", "pkg/x.go", true},
		{"lint", "README.md", false},
		{"format", ".golangci.yaml", true},
		{"format", "pkg/x.go", true},
		{"format", "README.md", false},
	} {
		h, ok := hooks[tc.hook]
		if !ok {
			t.Fatalf("no %s hook written:\n%s", tc.hook, written)
		}
		if got := h.selects(tc.path); got != tc.want {
			t.Errorf("%s selects %s = %v, want %v (files=%q types=%v types_or=%v)", tc.hook, tc.path, got, tc.want, h.Files, h.Types, h.TypesOr)
		}
	}
}

func TestGoProfileFiltersDoNotTriggerAuditScopeNote(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{"go.mod": goModFile, "go.sum": ""})
	apply(t, d)
	for _, n := range report(t, d).Notes {
		if strings.HasSuffix(n.ID, ":scope") {
			t.Fatalf("audit scope note on written config: %+v", n)
		}
	}
}

func TestTypeScriptTestHookSelectsLockfileChanges(t *testing.T) {
	fakePrek(t)
	d := tmp(t, map[string]string{
		"package.json": `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"1.0.0"}}`, "bun.lock": "", "tsconfig.json": "{}",
	})
	_, written := apply(t, d)
	h, ok := writtenHooks(t, written)["test"]
	if !ok {
		t.Fatalf("no test hook:\n%s", written)
	}
	for p, want := range map[string]bool{"bun.lock": true, "pnpm-lock.yaml": true, "package.json": true, "package-lock.json": true, "web/package-lock.json": true, "yarn.lock": true, "src/a.ts": true, "README.md": false} {
		if got := h.selects(p); got != want {
			t.Errorf("test selects %s = %v, want %v (files=%q types_or=%v)", p, got, want, h.Files, h.TypesOr)
		}
	}
}
