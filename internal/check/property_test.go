package check

import (
	"slices"
	"testing"

	"pgregory.net/rapid"
)

// Names start with "z" so no generated path can be a manifest, lockfile,
// quality-config or task-runner file.
func affectedPath() *rapid.Generator[string] {
	return rapid.StringMatching(`([a-y]{1,5}/){0,3}z[a-y0-9]{0,6}\.(go|ts|md|py|txt)`)
}

var fullTriggers = []string{
	"go.mod", "go.sum", "package.json", "pnpm-lock.yaml", "bun.lockb", "Cargo.lock",
	"verify.yaml", ".golangci.yml", ".pre-commit-config.yaml", "tsconfig.json", "biome.json",
	"Makefile", "justfile", "rules.mk", "Taskfile.yml",
}

func TestProp_AddingManifestLockfileOrQualityConfigPathYieldsFullScope(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		paths := rapid.SliceOf(affectedPath()).Draw(t, "paths")
		trigger := rapid.SampledFrom(fullTriggers).Draw(t, "trigger")
		dir := rapid.SampledFrom([]string{"", "web/", "a/b/"}).Draw(t, "dir")
		paths = slices.Insert(paths, rapid.IntRange(0, len(paths)).Draw(t, "at"), dir+trigger)
		if scope, reason := computeScope("mb", paths); scope != ScopeFull || reason == "" {
			t.Fatalf("scope %q reason %q for %v", scope, reason, paths)
		}
	})
}

func TestProp_AddingWorkflowFileYieldsFullScope(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		paths := rapid.SliceOf(affectedPath()).Draw(t, "paths")
		wf := ".github/workflows/" + affectedPath().Draw(t, "workflow")
		paths = slices.Insert(paths, rapid.IntRange(0, len(paths)).Draw(t, "at"), wf)
		if scope, reason := computeScope("mb", paths); scope != ScopeFull || reason == "" {
			t.Fatalf("scope %q reason %q for %v", scope, reason, paths)
		}
	})
}

func TestProp_OnlyAffectedPathsYieldAffectedScope(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		paths := rapid.SliceOf(affectedPath()).Draw(t, "paths")
		if scope, reason := computeScope("mb", paths); scope != ScopeAffected || reason != "" {
			t.Fatalf("scope %q reason %q for %v", scope, reason, paths)
		}
	})
}
