// Package onboard implements `verify setup --apply` (SPEC §4.3.1). It fills in
// only what is missing: it never overwrites or reorders existing content.
package onboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/pkgmgr"
)

const prekFile = ".pre-commit-config.yaml"

// Result lists what Apply wrote and what it could not do.
type Result struct {
	Written []string
	Notes   []string
}

// hygiene is the builtin hook block written to a new prek config. Hooks are
// written in block style, which repository YAML formatters (oxfmt, prettier)
// leave unchanged; one-line flow mappings get reflowed and fail format checks.
var hygiene = [][]string{
	{"id: trailing-whitespace", "args: [--markdown-linebreak-ext=md]"},
	{"id: end-of-file-fixer"},
	{"id: check-merge-conflict"},
	{"id: check-added-large-files"},
	{"id: check-yaml"},
}

// hygieneBlock renders the builtin repo entry for a new prek config.
func hygieneBlock() string {
	var b strings.Builder
	b.WriteString("  - repo: builtin\n    hooks:\n")
	for _, h := range hygiene {
		// Fixers must not rewrite byte-exact fixtures under testdata/.
		for i, f := range append(slices.Clone(h), `exclude: (^|/)testdata/`, "priority: 0", "stages: [pre-commit]") {
			lead := "        "
			if i == 0 {
				lead = "      - "
			}
			b.WriteString(lead + f + "\n")
		}
	}
	return b.String()
}

// Apply onboards dir according to the audit report rep.
func Apply(ctx context.Context, dir string, rep audit.Report) (Result, error) {
	var res Result
	if rep.Profile == "" || rep.Profile == "unsupported" {
		return res, nil
	}
	if err := writePrek(ctx, dir, rep, &res); err != nil {
		return res, err
	}
	installHooks(ctx, dir, &res)
	return res, nil
}

func writeNew(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644) // #nosec G306 G703 -- tracked repository config, not a secret
}

func writePrek(ctx context.Context, dir string, rep audit.Report, res *Result) error {
	path, err := config.PrekConfig(dir)
	if err != nil {
		return err
	}
	if filepath.Base(path) == "prek.toml" {
		res.Notes = append(res.Notes, "prek.toml exists; add the hooks named by the audit to it by hand")
		return nil
	}
	exists := path != ""
	if !exists {
		path = filepath.Join(dir, prekFile)
	}
	var old []byte
	if exists {
		old, err = os.ReadFile(path) // #nosec G304 -- repository config selected by PrekConfig
		if err != nil {
			return err
		}
	}
	head, indent, used := "minimum_prek_version: \"0.5.0\"\nrepos:\n"+hygieneBlock(), 2, map[string]bool{}
	if exists {
		var ok bool
		if head, indent, used, ok = plan(string(old)); !ok {
			res.Notes = append(res.Notes, filepath.Base(path)+" is not a block-style list ending the file; add the hooks named by the audit by hand")
			return nil
		}
	}
	block, err := localBlock(dir, rep, used, indent, res)
	if err != nil {
		return err
	}
	if exists && block == "" {
		return nil
	}
	return commit(ctx, dir, path, old, head+block, res)
}

// commit writes text to path and checks prek accepts it, restoring old
// (or removing the new file) when it does not.
func commit(ctx context.Context, dir, path string, old []byte, text string, res *Result) error {
	if err := writeNew(path, []byte(text)); err != nil {
		return err
	}
	if p, err := exec.LookPath("prek"); err == nil {
		cmd := exec.CommandContext(ctx, p, "validate-config", path) // #nosec G204 -- prek found on PATH, fixed arguments
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			if old == nil {
				_ = os.Remove(path)
			} else {
				_ = writeNew(path, old)
			}
			return fmt.Errorf("prek rejected the generated %s (reverted): %s", filepath.Base(path), strings.TrimSpace(string(out)))
		}
	}
	res.Written = append(res.Written, filepath.Base(path))
	return nil
}

// plan checks that text ends with a block-style repos list (or an empty
// `repos: []`) that hooks can be appended to. It returns the text to append
// to, the column of the repo items, and the hook ids already present.
func plan(text string) (head string, indent int, used map[string]bool, ok bool) {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(text), &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", 0, nil, false
	}
	m := doc.Content[0].Content
	if len(m) < 2 || m[len(m)-2].Value != "repos" || m[len(m)-1].Kind != yaml.SequenceNode {
		return "", 0, nil, false
	}
	seq := m[len(m)-1]
	used = map[string]bool{}
	collectIDs(seq, used)
	head = text
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	switch {
	case len(seq.Content) == 0 && seq.Style&yaml.FlowStyle != 0:
		empty := regexp.MustCompile(`(?m)^repos:[ \t]*\[\s*\][ \t]*$`)
		if !empty.MatchString(head) {
			return "", 0, nil, false
		}
		return empty.ReplaceAllString(head, "repos:"), 2, used, true
	case len(seq.Content) > 0 && seq.Style&yaml.FlowStyle == 0:
		return head, max(seq.Content[0].Column-3, 0), used, true
	}
	return "", 0, nil, false
}

func collectIDs(n *yaml.Node, used map[string]bool) {
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 && c.Value == "id" && i+1 < len(n.Content) {
			used[n.Content[i+1].Value] = true
		}
		collectIDs(c, used)
	}
}

// def is the starter for a hook obligation, used when no entry point exists.
type def struct {
	starter, tool, files string
	types                []string // one type renders as types:, several as types_or:
	filenames            bool     // the starter is file-level and receives filenames
}

var tsTypes = []string{"javascript", "jsx", "ts", "tsx", "json"}

var goDefs = map[string]def{
	"format":     {starter: "golangci-lint fmt --diff", types: []string{"go"}},
	"lint":       {starter: "golangci-lint run", types: []string{"go"}},
	"tidy":       {starter: "go mod tidy -diff", files: `(^|/)go\.(mod|sum)$`},
	"test":       {starter: "go test -race ./...", types: []string{"go"}},
	"coverage":   {starter: "sh -c 'go test -race -coverprofile=coverage.out ./... && go-test-coverage --config=.testcoverage.yml'", types: []string{"go"}},
	"deadcode":   {starter: `sh -c 'out=$(deadcode -test ./...) || exit $?; test -z "$out" || { echo "$out"; exit 1; }'`, types: []string{"go"}},
	"vuln":       {starter: "govulncheck ./...", types: []string{"go"}},
	"actionlint": {starter: "actionlint", files: `^\.github/workflows/`},
}

var tsDefs = map[string]def{
	"format":     {starter: "oxfmt --check", tool: "oxfmt", types: tsTypes, filenames: true},
	"lint":       {starter: "oxlint --report-unused-disable-directives-severity=error", tool: "oxlint", types: tsTypes[:4], filenames: true},
	"typecheck":  {starter: "tsc --noEmit", tool: "typescript", types: tsTypes},
	"test":       {starter: "vitest run", tool: "vitest", types: tsTypes},
	"actionlint": goDefs["actionlint"],
}

// checkOnly lists obligations whose usual entry point fixes files (`make fmt`,
// `npm run format`); hooks must check, not fix (SPEC §4.5), so only a
// check-named entry point counts for them.
var checkOnly = map[string]bool{"format": true, "tidy": true}

// entryPoint finds an existing Make target or package script by name only:
// the standard id or an accepted alternative (optionally with a -check suffix).
func entryPoint(dir, id string, names []string) (string, error) {
	var cands []string
	for _, n := range names {
		if checkOnly[id] {
			cands = append(cands, n+"-check", n+":check", "check-"+n)
		} else {
			cands = append(cands, n, n+"-check")
		}
	}
	for _, f := range []string{"Makefile", "makefile", "GNUmakefile"} {
		b, err := os.ReadFile(filepath.Join(dir, f)) // #nosec G304 -- fixed names under the repo root
		if err != nil {
			continue
		}
		for _, n := range cands {
			if regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(n) + `:`).Match(b) {
				return "make " + n, nil
			}
		}
		break
	}
	sub, m := pkgmgr.Project(dir)
	p, err := pkgmgr.ReadPackage(filepath.Join(dir, sub))
	if err != nil || p == nil {
		return "", err
	}
	for _, n := range cands {
		if p.Scripts[n] != "" {
			return m.RunScript(sub, n), nil
		}
	}
	return "", nil
}

// wanted lists the unmet, unwaived REQUIRED hook obligations. One coverage
// hook meets the test obligation too, so no separate test hook is written.
func wanted(dir string, rep audit.Report) ([]string, error) {
	var ids []string
	for _, o := range rep.Obligations {
		if _, _, ok := audit.HookRule(o.ID); ok && o.Level == audit.Required && !o.Satisfied && !o.Waived {
			ids = append(ids, o.ID)
		}
	}
	if i := slices.Index(ids, "test"); i >= 0 {
		if slices.Contains(ids, "coverage") {
			ids = slices.Delete(ids, i, i+1)
		} else {
			_, names, _ := audit.HookRule("coverage")
			e, err := entryPoint(dir, "coverage", names)
			if err != nil {
				return nil, err
			}
			if e != "" {
				ids[i] = "coverage"
			}
		}
	}
	return ids, nil
}

func hookDef(profile, id string) (def, bool) {
	d, ok := goDefs[id]
	if profile == "typescript" || !ok {
		d, ok = tsDefs[id]
		return d, ok
	}
	return d, true
}

func starterEntry(entry string, ok bool, d def, pkg *pkgmgr.Package, m pkgmgr.Manager, sub, id, stage string, res *Result) (string, bool, bool) {
	switch {
	case entry != "":
		return entry, false, true
	case !ok:
		return "", false, false
	case d.tool == "":
		return d.starter, false, true
	case !pkg.Declares(d.tool):
		res.Notes = append(res.Notes, fmt.Sprintf("no %s hook written: %s is not a declared dependency, so `npx` would fetch an unrelated package; add a %s hook running %q once it is", id, d.tool, stage, d.starter))
		return "", false, false
	case sub != "":
		return fmt.Sprintf("sh -c 'cd %s && %s %s'", sub, m.Exec, d.starter), false, true
	default:
		return m.Exec + " " + d.starter, d.filenames, true
	}
}

func sharedHook(id string) bool {
	switch id {
	case "format", "lint", "test", "coverage":
		return true
	}
	return false
}

// localBlock renders a `repo: local` block (repo items at column indent) for
// the unmet hook obligations, skipping ids already used.
func localBlock(dir string, rep audit.Report, used map[string]bool, indent int, res *Result) (string, error) {
	ids, err := wanted(dir, rep)
	if err != nil {
		return "", err
	}
	pad := strings.Repeat(" ", indent)
	sub, m := pkgmgr.Project(dir)
	pkg, err := pkgmgr.ReadPackage(filepath.Join(dir, sub))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, id := range ids {
		stage, names, _ := audit.HookRule(id)
		if used[id] {
			res.Notes = append(res.Notes, "hook id "+id+" already exists in prek config but does not run at pre-commit or pre-push; fix it by hand")
			continue
		}
		d, ok := hookDef(rep.Profile, id)
		entry, err := entryPoint(dir, id, names)
		if err != nil {
			return "", err
		}
		if rep.Profile == "mixed" && sharedHook(id) && entry == "" {
			res.Notes = append(res.Notes, "no "+id+" hook written for mixed profile: add a shared entry point covering both languages")
			continue
		}
		entry, filenames, write := starterEntry(entry, ok, d, pkg, m, sub, id, stage, res)
		if !write {
			continue
		}
		used[id] = true
		if b.Len() == 0 {
			b.WriteString(pad + "- repo: local\n" + pad + "  hooks:\n")
		}
		for i, line := range hookFields(id, stage, entry, d, filenames) {
			lead := pad + "      "
			if i == 0 {
				lead = pad + "    - "
			}
			b.WriteString(lead + line + "\n")
		}
	}
	return b.String(), nil
}

func installHooks(ctx context.Context, dir string, res *Result) {
	const manual = "run `prek install --hook-type pre-commit --hook-type pre-push`"
	p, err := exec.LookPath("prek")
	if err != nil {
		res.Notes = append(res.Notes, "prek is not on PATH, so the Git hooks were not installed; "+manual)
		return
	}
	cmd := exec.CommandContext(ctx, p, "install", "--hook-type", "pre-commit", "--hook-type", "pre-push") // #nosec G204 -- prek found on PATH, fixed arguments
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		res.Notes = append(res.Notes, "prek install failed ("+strings.TrimSpace(string(out))+"); "+manual)
		return
	}
	res.Written = append(res.Written, "Git hook shims (prek install)")
}

// hookFields renders the YAML lines of one hook (SPEC §4.5 scoping).
func hookFields(id, stage, entry string, d def, filenames bool) []string {
	f := []string{"id: " + id, "name: " + id, "entry: " + strconv.Quote(entry), "language: system"}
	switch {
	case len(d.types) == 1:
		f = append(f, "types: ["+d.types[0]+"]")
	case len(d.types) > 1:
		f = append(f, "types_or: ["+strings.Join(d.types, ", ")+"]")
	}
	if d.files != "" {
		f = append(f, "files: '"+d.files+"'")
	}
	if !filenames {
		f = append(f, "pass_filenames: false")
	}
	return append(f, "priority: 10", "stages: ["+stage+"]")
}
