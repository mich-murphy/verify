package e2e

import (
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const tsFilter = "types_or: [ts, javascript]"

func tsFixture(dir string) map[string]string {
	return map[string]string{
		"package.json":       "{\n  \"name\": \"fixture\",\n  \"version\": \"1.0.0\",\n  \"private\": true,\n  \"type\": \"module\",\n  \"scripts\": {\n    \"test\": \"node --test src/index.test.mjs\",\n    \"typecheck\": \"node --check src/index.ts\"\n  }\n}\n",
		"package-lock.json":  "{\n  \"name\": \"fixture\",\n  \"version\": \"1.0.0\",\n  \"lockfileVersion\": 3,\n  \"requires\": true,\n  \"packages\": {\n    \"\": {\n      \"name\": \"fixture\",\n      \"version\": \"1.0.0\"\n    }\n  }\n}\n",
		"tsconfig.json":      "{\n  \"compilerOptions\": {\n    \"strict\": true,\n    \"noUncheckedIndexedAccess\": true,\n    \"exactOptionalPropertyTypes\": true,\n    \"noEmit\": true,\n    \"allowImportingTsExtensions\": true\n  },\n  \"include\": [\"src\"]\n}\n",
		"src/index.ts":       "export function add(a: number, b: number): number {\n  return a + b;\n}\n",
		"src/index.test.mjs": "import assert from \"node:assert\";\nimport { test } from \"node:test\";\nimport { add } from \"./index.ts\";\n\ntest(\"add\", () => {\n  assert.equal(add(1, 2), 3);\n});\n",
		"README.md":          "fixture\n",
		"verify.yaml":        waivers("format", "lint", "lint-unused-directives"),
		".pre-commit-config.yaml": prekConfig(
			hook(dir, "typecheck", "pre-commit", "node --check src/index.ts", tsFilter),
			hook(dir, "test", "pre-push", "node --test src/index.test.mjs", tsFilter),
		),
	}
}

// requireNodeTypeStripping skips when node cannot run .ts files natively (before 22.18).
func requireNodeTypeStripping(t *testing.T) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "node", "--version").Output() // #nosec G204 -- fixed command
	if err != nil {
		unavailable(t, "node not on PATH")
	}
	m := regexp.MustCompile(`^v(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		unavailable(t, "unrecognised node version %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 22 || major == 22 && minor < 18 {
		unavailable(t, "node %s does not strip types by default", strings.TrimSpace(string(out)))
	}
}

func TestTSSetupAudit(t *testing.T) {
	requireNodeTypeStripping(t)
	f := newFixture(t, tsFixture)
	r, code := f.json("setup")
	if code != 0 || r.Status != "pass" || !strings.Contains(r.Message, "typecheck: ok (hook typecheck)") || !strings.Contains(r.Message, "test: ok (hook test)") {
		t.Fatalf("code=%d %+v", code, r)
	}
	if !slices.Contains(r.rules(), "setup:hook-tiers") {
		t.Fatalf("typecheck at pre-commit should raise the hook-tiers note: %v", r.rules())
	}
}

func TestTSSourceChange(t *testing.T) {
	requireNodeTypeStripping(t)
	f := newFixture(t, tsFixture)
	f.commit("src/index.ts", "export function add(a: number, b: number): number {\n  return a + b;\n}\n\nexport const zero = 0;\n")
	r, code := f.json("check")
	if code != 0 || r.Status != "pass" || r.Lane != "standard" {
		t.Fatalf("code=%d %+v", code, r)
	}
	if got := r.stepNames(); !slices.Equal(got, []string{"prepare-deps", "prek-pre-commit", "prek-pre-push"}) {
		t.Fatalf("steps %v", got)
	}
}

func TestTSLockfileWithoutManifest(t *testing.T) {
	requireNodeTypeStripping(t)
	f := newFixture(t, tsFixture)
	lock := strings.Replace(tsFixture("")["package-lock.json"], "\"version\": \"1.0.0\"\n    }", "\"version\": \"1.0.1\"\n    }", 1)
	f.commit("package-lock.json", lock)
	r, code := f.json("check")
	if code != 0 || r.Lane != "signoff" {
		t.Fatalf("code=%d %+v", code, r)
	}
	for _, want := range []string{
		"scope: full (manifest or lockfile changed: package-lock.json)",
		"note: lock file removals or binary change cannot be ruled out: package-lock.json",
	} {
		if !strings.Contains(r.Message, want) {
			t.Fatalf("message %q lacks %q", r.Message, want)
		}
	}
}

func TestTSVerifyYamlChange(t *testing.T) {
	requireNodeTypeStripping(t)
	f := newFixture(t, tsFixture)
	f.commit("verify.yaml", tsFixture("")["verify.yaml"]+"# touched\n")
	r, code := f.json("check")
	if code != 0 || r.Lane != "signoff" {
		t.Fatalf("code=%d %+v", code, r)
	}
	fd, ok := r.finding("guard:config")
	if !ok || fd.Message != "quality config changed; the change needs sign-off" {
		t.Fatalf("finding %+v in %v", fd, r.rules())
	}
	if !strings.Contains(r.Message, "note: quality config changed: verify.yaml") {
		t.Fatalf("message %q", r.Message)
	}
}
