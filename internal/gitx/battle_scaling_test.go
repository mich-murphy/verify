package gitx

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func FuzzBattleQuotedHeader(f *testing.F) {
	for _, p := range []string{"space name", "tab\t", "line\nname", "café", `quote"back\slash`} {
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if p == "" {
			return
		}
		h := strconv.Quote("a/"+p) + " " + strconv.Quote("b/"+p)
		if got := headerPath(h); got != p {
			t.Fatalf("headerPath(%q)=%q want %q", h, got, p)
		}
		if got := diffPath(strconv.Quote("b/"+p), "b/"); got != p {
			t.Fatalf("diffPath lost %q: %q", p, got)
		}
	})
}

func BenchmarkBattleParseDiff(b *testing.B) {
	for _, files := range []int{100, 10000} {
		var text strings.Builder
		for i := 0; i < files; i++ {
			fmt.Fprintf(&text, "diff --git a/src/f%d.go b/src/f%d.go\n--- a/src/f%d.go\n+++ b/src/f%d.go\n@@ -1 +1 @@\n-before\n+after\n", i, i, i, i)
		}
		input := text.String()
		b.Run(fmt.Sprint(files), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				if _, err := ParseLineDiff(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
