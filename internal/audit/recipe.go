package audit

import (
	"regexp"
	"strings"
)

// MakeRecipe returns the recipe lines of target in Makefile text, joined by
// newlines, with comments dropped and backslash continuations folded. A
// missing target returns "". It does not expand variables or follow
// prerequisites.
func MakeRecipe(text, target string) string {
	var out []string
	in, joining := false, false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "\t"):
			if !in || strings.HasPrefix(t, "#") {
				continue
			}
			if joining {
				out[len(out)-1] += " " + t
			} else {
				out = append(out, t)
			}
			joining = strings.HasSuffix(t, `\`)
			if joining {
				out[len(out)-1] = strings.TrimSpace(strings.TrimSuffix(out[len(out)-1], `\`))
			}
		case t == "" || strings.HasPrefix(t, "#"):
		default:
			in, joining = definesTarget(line, target), false
		}
	}
	return strings.Join(out, "\n")
}

func definesTarget(line, target string) bool {
	i := strings.Index(line, ":")
	if i < 0 || strings.Contains(line[:i], "=") || strings.HasPrefix(line[i+1:], "=") || strings.HasPrefix(line[i+1:], ":=") {
		return false
	}
	return strings.Contains(" "+strings.Join(strings.Fields(line[:i]), " ")+" ", " "+target+" ")
}

var uncachedFlag = regexp.MustCompile(`(^|\s)-(count=1|shuffle)\b`)

// Uncached reports whether a command carries -count=1 or -shuffle, which stop
// the Go test cache from replaying a pass.
func Uncached(cmd string) bool { return uncachedFlag.MatchString(cmd) }
