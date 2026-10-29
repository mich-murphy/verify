package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mich-murphy/verify/internal/pkgmgr"
)

// stripJSONC removes comments and trailing commas, respecting strings.
func stripJSONC(in []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(in); i++ {
		switch {
		case in[i] == '"':
			j := stringEnd(in, i)
			out.Write(in[i:min(j+1, len(in))])
			i = j
		case in[i] == '/' && i+1 < len(in) && (in[i+1] == '/' || in[i+1] == '*'):
			i = commentEnd(in, i)
			out.WriteByte('\n')
		default:
			out.WriteByte(in[i])
		}
	}
	// Strip trailing commas only outside quoted strings.
	in = out.Bytes()
	out.Reset()
	for i := 0; i < len(in); i++ {
		if in[i] == '"' {
			j := stringEnd(in, i)
			out.Write(in[i:min(j+1, len(in))])
			i = j
			continue
		}
		if in[i] == ',' {
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\r' || in[j] == '\n') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				continue
			}
		}
		out.WriteByte(in[i])
	}
	return out.Bytes()
}

// stringEnd returns the index of the quote closing the string opened at i.
func stringEnd(in []byte, i int) int {
	j := i + 1
	for j < len(in) && in[j] != '"' {
		if in[j] == '\\' {
			j++
		}
		j++
	}
	return j
}

// commentEnd returns the last index of the comment starting at i.
func commentEnd(in []byte, i int) int {
	if in[i+1] == '/' {
		for i < len(in) && in[i] != '\n' {
			i++
		}
		return i
	}
	for i += 2; i+1 < len(in) && (in[i] != '*' || in[i+1] != '/'); i++ {
	}
	return i + 1
}

// tsFlags returns the effective boolean compiler options of rel, following
// relative `extends` (the child overrides its parents).
func (a *auditor) tsFlags(rel string, depth int) map[string]bool {
	flags := map[string]bool{}
	text, ok := a.read(rel)
	if !ok || depth > 5 {
		return flags
	}
	var c struct {
		Extends         any            `json:"extends"`
		CompilerOptions map[string]any `json:"compilerOptions"`
	}
	if err := json.Unmarshal(stripJSONC([]byte(text)), &c); err != nil {
		a.setErr(fmt.Errorf("cannot parse %s: %w", rel, err))
		return flags
	}
	parents, _ := c.Extends.([]any)
	if s, ok := c.Extends.(string); ok {
		parents = []any{s}
	}
	for _, v := range parents {
		if p, _ := v.(string); strings.HasPrefix(p, ".") {
			if !strings.HasSuffix(p, ".json") {
				p += ".json"
			}
			for k, v := range a.tsFlags(path.Join(path.Dir(rel), p), depth+1) {
				flags[k] = v
			}
		}
	}
	for k, v := range c.CompilerOptions {
		if b, ok := v.(bool); ok {
			flags[k] = b
		}
	}
	return flags
}

var (
	exactVersion = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][\w.+-]+)?$`)
	// unusedDirectives matches the linter settings that make an unused
	// disable directive an error (oxlint, eslint, biome).
	unusedDirectives = regexp.MustCompile(`(?i)reportUnusedDisableDirectives["']?\s*:\s*["']?(error|2)\b|noUnusedSuppressions|suppressions/unused`)
	// unusedFlag is the same setting given on the lint script's command line.
	unusedFlag    = regexp.MustCompile(`--report-unused-disable-directives(-severity[= ]error)?\b`)
	linterConfigs = []string{".oxlintrc.json", "oxlint.json", "eslint.config.js", "eslint.config.mjs", "eslint.config.ts", "eslint.config.cjs", "biome.json", "biome.jsonc"}
)

// lockProblem checks the lockfile and exact tool versions of project dir d.
func (a *auditor) lockProblem(d string) string {
	p, err := pkgmgr.ReadPackage(filepath.Join(a.dir, d))
	if err != nil {
		a.setErr(err)
		return ""
	}
	if !slices.ContainsFunc(pkgmgr.Managers, func(m pkgmgr.Manager) bool { return a.exists(path.Join(d, m.Lock)) || a.exists(m.Lock) }) {
		return "no lockfile next to " + path.Join(d, "package.json")
	}
	var loose []string
	for n, v := range p.DevDependencies {
		if !exactVersion.MatchString(v) && !strings.HasPrefix(v, "workspace:") {
			loose = append(loose, n+"@"+v)
		}
	}
	if slices.Sort(loose); len(loose) > 0 && p.PackageManager == "" {
		return "devDependencies are not pinned to exact versions: " + strings.Join(loose, ", ")
	}
	return ""
}

func (a *auditor) tsFiles(d string) {
	ts := path.Join(d, "tsconfig.json")
	flags := a.tsFlags(ts, 0)
	problem := ""
	switch {
	case !flags["strict"]:
		problem = ts + ` must set "strict": true`
	case !flags["noUncheckedIndexedAccess"]:
		problem = ts + ` must set "noUncheckedIndexedAccess": true`
	}
	a.add("tsconfig", Required, problem, `set compilerOptions "strict" and "noUncheckedIndexedAccess" to true`)
	a.add("exact-optional", Recommended, problemIf(!flags["exactOptionalPropertyTypes"], "exactOptionalPropertyTypes is not enabled"),
		`set compilerOptions "exactOptionalPropertyTypes": true`)

	unused := false
	for _, dir := range []string{d, "."} {
		for _, n := range linterConfigs {
			if text, ok := a.read(path.Join(dir, n)); ok && unusedDirectives.MatchString(text) {
				unused = true
			}
		}
	}
	// The flag on the project's own `lint` script counts too; this reads that
	// one script's text, it does not expand what it calls.
	if p, err := pkgmgr.ReadPackage(filepath.Join(a.dir, d)); err == nil && p != nil && unusedFlag.MatchString(p.Scripts["lint"]) {
		unused = true
	}
	a.add("lint-unused-directives", Required, problemIf(!unused, "no linter config reports unused disable directives as errors"),
		`oxlint: "options": {"reportUnusedDisableDirectives": "error"} in .oxlintrc.json; eslint: linterOptions.reportUnusedDisableDirectives: "error"; biome: suppressions/unused`)
	a.add("lockfile", Required, a.lockProblem(d), "commit the package manager lockfile and pin devDependencies to exact versions (or set packageManager)")
}
