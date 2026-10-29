package glob

import (
	"fmt"
	"path"
	"strings"
	"testing"
)

// Independent bottom-up oracle: each cell means pattern/name prefixes match.
func battleDP(pattern, name string) bool {
	p, n := strings.Split(pattern, "/"), strings.Split(name, "/")
	table := make([][]bool, len(p)+1)
	for i := range table {
		table[i] = make([]bool, len(n)+1)
	}
	table[0][0] = true
	for i, seg := range p {
		for j := 0; j <= len(n); j++ {
			if seg == "**" {
				table[i+1][j] = table[i][j] || j > 0 && table[i+1][j-1]
			} else if j > 0 {
				ok, err := path.Match(seg, n[j-1])
				table[i+1][j] = err == nil && ok && table[i][j-1]
			}
		}
	}
	return table[len(p)][len(n)]
}

func TestBattleGlobMatchesDP(t *testing.T) {
	patterns := []string{"a", "b", "*", "**", "?", "[ab]"}
	names := []string{"a", "b", "c"}
	var expand func([]string, []string, int) []string
	expand = func(out, alphabet []string, depth int) []string {
		if depth == 0 {
			return out
		}
		prefix := append([]string(nil), out...)
		for _, s := range prefix {
			for _, a := range alphabet {
				out = append(out, s+"/"+a)
			}
		}
		return expand(out, alphabet, depth-1)
	}
	for _, p := range expand(append([]string(nil), patterns...), patterns, 2) {
		for _, n := range expand(append([]string(nil), names...), names, 2) {
			if got, want := Match(p, n), battleDP(p, n); got != want {
				t.Fatalf("Match(%q,%q)=%v want %v", p, n, got, want)
			}
		}
	}
}

func BenchmarkBattleGlob(b *testing.B) {
	for _, depth := range []int{4, 8, 12} {
		name := strings.Repeat("segment/", depth) + "file.go"
		for _, pattern := range []string{"src/**", "**/*.go", "**/**/**/**/**/**/missing"} {
			b.Run(fmt.Sprintf("depth%d/%s", depth, pattern), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = Match(pattern, name)
				}
			})
		}
	}
}
