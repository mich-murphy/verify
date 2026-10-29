package guard

import (
	"path"
	"regexp"
	"strings"

	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/lane"
	"github.com/mich-murphy/verify/internal/state"
)

// The pattern strings are assembled by concatenation so that this source file
// never contains the literal suppressions it searches for and so does not
// trip Gate's own suppressions guard. Test fixtures do the same.
var (
	lintPatterns = []string{
		"//" + "nolint", "eslint" + "-disable", "oxlint" + "-disable", "biome" + "-ignore", "# " + "noqa",
		"@ts" + "-ignore", "@ts" + "-expect-error", "@ts" + "-nocheck",
	}
	goTestPattern = regexp.MustCompile(`\bt\.Sk` + `ip(f|Now)?\(`)
	jsTestPattern = regexp.MustCompile(`\.sk` + `ip\(|\.on` + `ly\(|\bx` + `it\(|\bxdes` + `cribe\(`)
)

var jsExts = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true,
}

func suppressions(d *gitx.LineDiff) []state.Finding {
	var out []state.Finding
	for _, l := range d.Added {
		if lane.IsDocs(l.Path) {
			continue
		}
		ext := path.Ext(l.Path)
		what := suppressionIn(l.Text)
		switch {
		case what != "":
		case ext == ".go" && goTestPattern.MatchString(l.Text):
			what = "skipped test"
		case jsExts[ext] && jsTestPattern.MatchString(l.Text):
			what = "skipped or focused test"
		default:
			continue
		}
		out = append(out, finding(Suppressions, l.Path, l.Number, state.SeverityBlock,
			"added "+what+"; fix the cause or record `gate decide accept`"))
	}
	return out
}

func suppressionIn(text string) string {
	for _, p := range lintPatterns {
		if strings.Contains(text, p) {
			return "suppression (" + p + ")"
		}
	}
	return ""
}
