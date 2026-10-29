package lane

import (
	"fmt"
	"testing"

	"github.com/mich-murphy/verify/internal/config"
)

func BenchmarkBattleLanePaths(b *testing.B) {
	for _, size := range []int{100, 10000} {
		changes := make([]Change, size)
		for i := range changes {
			changes[i] = Change{Path: fmt.Sprintf("internal/pkg%d/file.go", i), Added: 1}
		}
		cfg := config.Config{SignoffPaths: []string{"cmd/**", "internal/state/**", "integrations/**"}}
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = Compute(changes, cfg, false)
			}
		})
	}
}
