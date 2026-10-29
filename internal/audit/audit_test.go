package audit

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/config"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for n, c := range files {
		p := filepath.Join(d, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	//nolint:gosec,noctx // fixed git arguments in a test
	if out, err := exec.Command("git", "-C", d, "init", "-q").CombinedOutput(); err != nil {
		t.Skip("git unavailable", string(out))
	}
	return d
}

func get(t *testing.T, r Report, id string) Obligation {
	t.Helper()
	for _, o := range r.Obligations {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("no obligation %s", id)
	return Obligation{}
}

func TestMatches(t *testing.T) {
	n := []string{"lint", "oxlint"}
	for id, want := range map[string]bool{"lint": true, "web-oxlint": true, "lint-full": true, "linter": false, "splint": false} {
		if Matches(id, n) != want {
			t.Errorf("%s: want %v", id, want)
		}
	}
}

func TestNoProfileAndFindings(t *testing.T) {
	if r, err := Run(t.Context(), t.TempDir(), config.Default()); err != nil || r.Profile != "unsupported" || !r.Blocking() || get(t, r, "prek-config").Satisfied {
		t.Fatal(r, err)
	}
	r := Report{Obligations: []Obligation{{ID: "a", Level: Required, Detail: "d"}, {ID: "b", Level: Recommended}, {ID: "c", Level: Required, Waived: true}}}
	if f := r.Findings(); len(f) != 2 || !r.Blocking() {
		t.Fatal(f)
	}
}

func TestAuditPrekPrecedenceAndYML(t *testing.T) {
	if _, err := exec.LookPath("prek"); err != nil {
		t.Skip("prek missing")
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		hook  string
	}{
		{"yml", map[string]string{".pre-commit-config.yml": "repos:\n  - repo: local\n    hooks:\n      - {id: lint, name: lint, entry: true, language: system, stages: [pre-commit]}\n"}, "lint"},
		{"toml before yaml", map[string]string{".pre-commit-config.yaml": "repos: []\n", "prek.toml": "[[repos]]\nrepo = 'local'\n[[repos.hooks]]\nid = 'lint'\nname = 'lint'\nentry = 'true'\nlanguage = 'system'\nstages = ['pre-commit']\n"}, "lint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.files["go.mod"] = "module x\n\ngo 1.26.1\n"
			d := repo(t, tc.files)
			r, err := Run(t.Context(), d, config.Default())
			if err != nil || !get(t, r, tc.hook).Satisfied {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestPrekOutputAndCancellation(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"overflow", "head -c 1048580 /dev/zero", "exceeded 1 MiB"},
		{"cancel", "sleep 10", "deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := repo(t, map[string]string{".pre-commit-config.yaml": "repos: []\n"})
			bin := t.TempDir()
			// #nosec G306 -- Private executable fixture under t.TempDir needs owner execute permission.
			if err := os.WriteFile(filepath.Join(bin, "prek"), []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, found, err := listHooks(ctx, d)
			if !found || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("found=%v err=%v", found, err)
			}
		})
	}
}

func TestMissingPrekConfigBlocks(t *testing.T) {
	d := repo(t, map[string]string{"go.mod": "module x\n\ngo 1.26.1\n"})
	r, err := Run(t.Context(), d, config.Default())
	if err != nil || get(t, r, "prek-config").Satisfied || !r.Blocking() {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestHooksMatchedByID(t *testing.T) {
	if _, err := exec.LookPath("prek"); err != nil {
		t.Skip("prek missing")
	}
	d := repo(t, map[string]string{
		"go.mod": "module x\n\ngo 1.26.1\n",
		".pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n" +
			"      - {id: golangci-lint-full, name: l, entry: true, language: system, stages: [pre-commit]}\n" +
			"      - {id: actionlint, name: a, entry: true, language: system, stages: [pre-commit]}\n" +
			"      - {id: go-lint, name: g, entry: true, language: system, stages: [pre-push]}\n" +
			"      - {id: cover, name: c, entry: true, language: system, stages: [pre-commit]}\n" +
			"      - {id: vuln, name: v, entry: true, language: system, stages: [manual]}\n",
	})
	r, err := Run(t.Context(), d, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if o := get(t, r, "lint"); !o.Satisfied || o.Hook != "go-lint, golangci-lint-full" {
		t.Fatalf("%+v", o)
	}
	if o := get(t, r, "test"); !o.Satisfied || o.Hook != "cover" {
		t.Fatalf("%+v", o)
	}
	if get(t, r, "vuln").Satisfied {
		t.Fatal("manual hook must not count")
	}
	if get(t, r, "hook-tiers").Satisfied {
		t.Fatal("coverage at pre-commit should raise hook-tiers")
	}
}

func TestTypeScriptFiles(t *testing.T) {
	d := repo(t, map[string]string{
		"package.json":      `{"devDependencies":{"oxlint":"1.0.0"}}`,
		"package-lock.json": "{}",
		"tsconfig.json":     `{"compilerOptions":{"strict":true,"noUncheckedIndexedAccess":true}} `,
		".oxlintrc.json":    `{"options":{"reportUnusedDisableDirectives":"error"}}`,
	})
	r, err := Run(t.Context(), d, config.Default())
	if err != nil || r.Profile != "typescript" {
		t.Fatal(r.Profile, err)
	}
	for _, id := range []string{"tsconfig", "lint-unused-directives", "lockfile"} {
		if o := get(t, r, id); !o.Satisfied {
			t.Fatalf("%+v", o)
		}
	}
	if get(t, r, "exact-optional").Satisfied {
		t.Fatal("exactOptionalPropertyTypes not set")
	}
}

func TestUnusedDirectivesFromLintScript(t *testing.T) {
	d := repo(t, map[string]string{
		"package.json":      `{"scripts":{"lint":"oxlint --report-unused-disable-directives-severity=error"},"devDependencies":{"oxlint":"1.0.0"}}`,
		"package-lock.json": "{}",
		"tsconfig.json":     `{"compilerOptions":{"strict":true,"noUncheckedIndexedAccess":true}}`,
		".oxlintrc.json":    `{"rules":{"no-unused-vars":"error"}}`,
	})
	r, err := Run(t.Context(), d, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if o := get(t, r, "lint-unused-directives"); !o.Satisfied {
		t.Fatalf("lint script flag should count: %+v", o)
	}
}

func TestStripJSONC(t *testing.T) {
	input := []byte(`{"compilerOptions":{"strict":true,"noUncheckedIndexedAccess":true,}, "description":"literal , } and , ] inside string", "escaped":"\\\", }", /*comment*/}`)
	var parsed map[string]any
	if err := json.Unmarshal(stripJSONC(input), &parsed); err != nil || parsed["description"] != "literal , } and , ] inside string" {
		t.Fatalf("%s: %v", stripJSONC(input), err)
	}
	malformed := []byte(`{"compilerOptions":{"strict":true},"description":"unterminated}`)
	if json.Valid(stripJSONC(malformed)) {
		t.Fatal("malformed input must not become valid")
	}
}

func TestTypeScriptWithoutTSConfigBlocks(t *testing.T) {
	d := repo(t, map[string]string{"package.json": `{}`})
	r, err := Run(t.Context(), d, config.Default())
	if err != nil || r.Profile != "typescript" || get(t, r, "tsconfig").Satisfied || get(t, r, "prek-config").Satisfied || !r.Blocking() {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestMalformedTSConfigFailsClosed(t *testing.T) {
	d := repo(t, map[string]string{"package.json": `{}`, "tsconfig.json": `{"compilerOptions": {"strict": true,}, "bad": "unfinished}`})
	if _, err := Run(t.Context(), d, config.Default()); err == nil {
		t.Fatal("malformed JSONC must return an audit error")
	}
}

func TestNestedTypeScriptHookIDs(t *testing.T) {
	if _, err := exec.LookPath("prek"); err != nil {
		t.Skip("prek missing")
	}
	d := t.TempDir()
	if err := os.CopyFS(d, os.DirFS("../../testdata/audit/nested-ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(d, "dot-pre-commit-config.yaml"), filepath.Join(d, ".pre-commit-config.yaml")); err != nil {
		t.Fatal(err)
	}
	d, _ = filepath.EvalSymlinks(d)
	//nolint:gosec,noctx // fixed git arguments in a test
	if err := exec.Command("git", "-C", d, "init", "-q").Run(); err != nil {
		t.Skip("git unavailable")
	}
	r, err := Run(t.Context(), d, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	for id, hook := range map[string]string{"format": "nested-oxfmt", "lint": "nested-oxlint", "typecheck": "nested-typecheck", "test": "nested-test"} {
		if o := get(t, r, id); !o.Satisfied || o.Hook != hook {
			t.Errorf("%s: %+v", id, o)
		}
	}
}

func TestGoProjectHasNoPrekConfig(t *testing.T) {
	r, err := Run(t.Context(), "../../testdata/audit/go-project", config.Default())
	if err != nil || r.Profile != "go" || get(t, r, "prek-config").Satisfied {
		t.Fatalf("%+v %v", r, err)
	}
}
