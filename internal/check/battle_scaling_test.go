package check

import (
	"fmt"
	"testing"
)

func BenchmarkBattleScopePaths(b *testing.B) {
	for _, size := range []int{100, 10000} {
		paths := make([]string, size)
		for i := range paths {
			paths[i] = fmt.Sprintf("internal/pkg%d/file.go", i)
		}
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = computeScope("base", paths)
			}
		})
	}
}
