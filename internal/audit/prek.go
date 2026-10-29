package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/runner"
)

const prekTimeout = 60 * time.Second

// ErrUnsettled means audit subprocess cleanup could not establish safe reuse.
var ErrUnsettled = errors.New("audit subprocess cleanup incomplete")

// hook is one entry of `prek list --output-format json`.
type hook struct {
	ID     string   `json:"id"`
	Stages []string `json:"stages"`
}

func (h hook) at(stage string) bool { return slices.Contains(h.Stages, stage) }

// runs reports whether the hook runs at commit or push; manual hooks do not count.
func (h hook) runs() bool { return h.at("pre-commit") || h.at("pre-push") }

// listHooks runs `prek list`. found reports whether dir has a prek config.
func listHooks(ctx context.Context, dir string) (hooks []hook, found bool, err error) {
	path, err := config.PrekConfig(dir)
	if err != nil {
		return nil, true, err
	}
	if path == "" {
		return nil, false, errors.New("no prek config found (prek.toml, .pre-commit-config.yaml or .pre-commit-config.yml)")
	}
	rn, err := runner.New(runner.Config{Env: runner.RepositoryEnv()})
	if err != nil {
		return nil, true, err
	}
	res, err := rn.Run(ctx, runner.Command{
		Args: []string{"prek", "list", "--config", path, "--output-format", "json"},
		Dir:  dir, Timeout: prekTimeout,
	})
	if err != nil {
		return nil, true, fmt.Errorf("prek list: %w", err)
	}
	if !res.Settled {
		return nil, true, fmt.Errorf("%w: %s", ErrUnsettled, res.Error)
	}
	if res.Status != runner.Pass {
		return nil, true, fmt.Errorf("prek list failed: %s: %s", res.Error, strings.TrimSpace(res.Stderr.Output))
	}
	if res.Stdout.Truncated {
		return nil, true, errors.New("prek list output exceeded 1 MiB")
	}
	if jerr := json.Unmarshal([]byte(res.Stdout.Output), &hooks); jerr != nil {
		return nil, true, fmt.Errorf("cannot parse `prek list` output: %w", jerr)
	}
	return hooks, true, nil
}

// rule is a hook obligation: the standard ID first, then accepted alternatives.
type rule struct {
	id, stage    string
	goLvl, tsLvl string
	alts         string
	full         bool // full tier: belongs at pre-push (SPEC §4.5)
}

const (
	commit = "pre-commit"
	push   = "pre-push"
)

var rules = []rule{
	{"format", commit, Required, Required, "fmt gofumpt oxfmt prettier biome-format dprint", false},
	{"lint", commit, Required, Required, "golangci-lint oxlint eslint biome-lint", false},
	{"tidy", push, Required, "", "go-mod-tidy", false},
	{"typecheck", push, "", Required, "tsc tsgo type-check check-types", true},
	{"test", push, Required, Required, "tests coverage cover", true}, // a coverage hook runs the tests
	{"coverage", push, Required, Recommended, "cover", true},
	{"deadcode", push, Required, Recommended, "dead-code fallow knip", true},
	{"vuln", push, Required, "", "govulncheck", true},
	{"actionlint", commit, Required, Recommended, "", false},
	{"fix", push, Recommended, "", "go-fix", false},
	{"dep-audit", push, "", Recommended, "audit npm-audit", false},
}

func (r rule) names() []string { return append([]string{r.id}, strings.Fields(r.alts)...) }

// level is the stricter of the levels of the languages present.
func (r rule) level(goP, tsP bool) string {
	var lv []string
	if goP {
		lv = append(lv, r.goLvl)
	}
	if tsP {
		lv = append(lv, r.tsLvl)
	}
	switch {
	case slices.Contains(lv, Required):
		return Required
	case slices.Contains(lv, Recommended):
		return Recommended
	}
	return ""
}

// HookRule returns the stage and accepted hook ids (standard id first) of a
// hook obligation; ok is false for other obligations.
func HookRule(id string) (stage string, names []string, ok bool) {
	for _, r := range rules {
		if r.id == id {
			return r.stage, r.names(), true
		}
	}
	return "", nil, false
}

// Matches reports whether id equals one of names or has one as a
// `-`-separated prefix or suffix (web-oxlint, golangci-lint-full).
func Matches(id string, names []string) bool {
	id = strings.ToLower(id)
	return slices.ContainsFunc(names, func(n string) bool {
		return id == n || strings.HasPrefix(id, n+"-") || strings.HasSuffix(id, "-"+n)
	})
}

// matching returns every hook that runs at commit or push and whose id names
// the obligation, so `verify setup` can show all of them and a wrong match
// (tofu-fmt for format) is visible next to the right one.
func matching(hooks []hook, names []string) []hook {
	var out []hook
	for _, h := range hooks {
		if h.runs() && Matches(h.ID, names) {
			out = append(out, h)
		}
	}
	return out
}
