// Package lane computes the SPEC §3 lane as a pure function of the diff and config.
package lane

import (
	"path"
	"strings"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/glob"
)

// Lane names.
const (
	Docs     = "docs"
	Standard = "standard"
	Signoff  = "signoff"
)

// Change is one changed file with line counts.
type Change struct {
	Path           string
	Added, Deleted int
	Binary         bool
}

// Result is the lane with the reasons that raised it to Sign-off.
type Result struct {
	Lane    string
	Reasons []string
}

var lockfiles = map[string]bool{
	"go.sum": true, "npm-shrinkwrap.json": true, "deno.lock": true, "package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
	"bun.lock": true, "bun.lockb": true, "Cargo.lock": true, "uv.lock": true, "poetry.lock": true,
}

var qualityBases = []string{
	"gate.yaml", ".pre-commit-config.yaml", ".pre-commit-config.yml", "prek.toml", ".golangci.yml", ".golangci.yaml",
	".golangci.toml", ".golangci.json", ".testcoverage.yml", ".testcoverage.yaml", ".eslintrc",
	".oxlintrc.json", "oxlint.json", "biome.json", "biome.jsonc", ".prettierrc", ".editorconfig",
	"deno.json", "knip.json", "knip.jsonc", ".fallowrc.json", "tox.ini", "ruff.toml", ".ruff.toml",
}

var qualityPrefixes = []string{".golangci", "tsconfig", ".eslintrc", "eslint.config", ".prettierrc", "prettier.config", "vitest.config", "jest.config", "coverage", ".fallowrc"}

// IsManifestOrLockfile identifies changes that demand a full check.
func IsManifestOrLockfile(p string) bool {
	b := path.Base(p)
	return b == "go.mod" || b == "package.json" || lockfiles[b]
}

// IsDocs reports whether a path is documentation-only.
func IsDocs(p string) bool {
	base := path.Base(p)
	return strings.HasSuffix(p, ".md") || p == "docs" || strings.HasPrefix(p, "docs/") || strings.HasPrefix(base, "CHANGELOG")
}

// IsQualityConfig reports whether a path is gate, prek, lint, format, type,
// coverage or CI configuration.
func IsQualityConfig(p string) bool {
	if strings.HasPrefix(p, ".github/workflows/") {
		return true
	}
	base := path.Base(p)
	for _, b := range qualityBases {
		if base == b {
			return true
		}
	}
	for _, pre := range qualityPrefixes {
		if strings.HasPrefix(base, pre) {
			return true
		}
	}
	return false
}

// Compute returns the lane. configFromCandidate means gate.yaml was absent or invalid at
// the merge-base, so the candidate's own copy governs and must be signed off.
func Compute(changes []Change, cfg config.Config, configFromCandidate bool) Result {
	var reasons []string
	lines, allDocs := 0, len(changes) > 0
	for _, c := range changes {
		lines += c.Added + c.Deleted
		if !IsDocs(c.Path) {
			allDocs = false
		}
		switch {
		case IsQualityConfig(c.Path):
			reasons = append(reasons, "quality config changed: "+c.Path)
		case lockfiles[path.Base(c.Path)] && (c.Deleted > 0 || c.Binary):
			reasons = append(reasons, "lock file removals or binary change cannot be ruled out: "+c.Path)
		case glob.MatchAny(cfg.SignoffPaths, c.Path):
			reasons = append(reasons, "critical path changed: "+c.Path)
		}
	}
	if configFromCandidate {
		reasons = append(reasons, "gate.yaml absent or invalid at merge-base; candidate copy used")
	}
	if lines > config.SignoffLines {
		reasons = append(reasons, "diff exceeds 400 changed lines")
	}
	switch {
	case len(reasons) > 0:
		return Result{Lane: Signoff, Reasons: reasons}
	case allDocs:
		return Result{Lane: Docs}
	}
	return Result{Lane: Standard}
}
