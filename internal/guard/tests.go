package guard

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/state"
)

var (
	goDecl    = regexp.MustCompile(`^\s*func (Test|Fuzz)\w*\(`)
	goSubtest = regexp.MustCompile(`\bt\.Run\(\s*"([^"\n]+)"\s*,`)
	jsDecl    = regexp.MustCompile(`^\s*(it|test)(\.\w+)*\(`)
)

func isTestFile(p string) bool {
	base := path.Base(p)
	return strings.HasSuffix(p, "_test.go") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.HasPrefix(p, "__tests__/") || strings.Contains(p, "/__tests__/")
}

func countDecls(lines []gitx.Line) map[string]int {
	n := map[string]int{}
	for _, l := range lines {
		if isTestFile(l.Path) && (goDecl.MatchString(l.Text) || jsDecl.MatchString(l.Text) || (strings.HasSuffix(l.Path, "_test.go") && goSubtest.MatchString(l.Text))) {
			n[l.Path]++
		}
	}
	return n
}

func testsRemoved(d *gitx.LineDiff) []state.Finding {
	var out []state.Finding
	seen := map[string]bool{}
	for _, p := range d.DeletedFiles {
		if isTestFile(p) && !seen[p] {
			seen[p] = true
			out = append(out, finding(TestsRemoved, p, 0, state.SeverityBlock, "test file deleted"))
		}
	}
	added, removed := countDecls(d.Added), countDecls(d.Removed)
	for _, l := range d.Removed {
		if seen[l.Path] || removed[l.Path] <= added[l.Path] {
			continue
		}
		seen[l.Path] = true
		out = append(out, finding(TestsRemoved, l.Path, 0, state.SeverityBlock,
			fmt.Sprintf("net removal of test declarations or literal Go subtest cases (%d removed, %d added)", removed[l.Path], added[l.Path])))
	}
	return out
}
