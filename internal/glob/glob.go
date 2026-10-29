// Package glob matches slash-separated repository paths against patterns where
// "**" spans any number of path segments and other segments use path.Match.
package glob

import (
	"path"
	"strings"
)

// Match reports whether name matches pattern.
func Match(pattern, name string) bool {
	return match(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// MatchAny reports whether name matches any pattern.
func MatchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if Match(p, name) {
			return true
		}
	}
	return false
}

// Valid reports whether every segment of pattern is well formed.
func Valid(pattern string) bool {
	if pattern == "" {
		return false
	}
	for seg := range strings.SplitSeq(pattern, "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return false
		}
	}
	return true
}

func match(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(n); i++ {
				if match(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		if ok, err := path.Match(p[0], n[0]); err != nil || !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}
