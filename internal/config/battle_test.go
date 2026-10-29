package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBattleAmbiguousYAML(t *testing.T) {
	for _, input := range []string{"base: main\nbase: other\n", "verify:\n  scenarios:\n    - {name: x, run: [echo], exit: 0, unexpected: true}\n", "---\n{}\n---\n{}\n", "prepare:\n  - {name: x, run: [echo], timeout: -1s}\n"} {
		if _, err := Parse([]byte(input)); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) error=%v, want ErrInvalid", input, err)
		}
	}
}

func FuzzBattleParse(f *testing.F) {
	for _, s := range []string{"", "{}", "base: main", "verify:\n  surfaces: ['cmd/**']\n  scenarios: [{name: help, run: [echo, ''], stdout_equals: ''}]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 1<<16 {
			return
		}
		_, err := Parse([]byte(s))
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Fatalf("unclassified parse error: %v", err)
		}
	})
}

func BenchmarkBattleParse(b *testing.B) {
	for _, n := range []int{1, 10, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			var text strings.Builder
			text.WriteString("verify:\n  surfaces: ['cmd/**']\n  scenarios:\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&text, "    - {name: scenario-%d, run: [echo, ok], stdout_equals: ok}\n", i)
			}
			data := []byte(text.String())
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := Parse(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
