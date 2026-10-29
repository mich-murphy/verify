package lane

import (
	"testing"

	"github.com/mich-murphy/verify/internal/config"
)

func TestCompute(t *testing.T) {
	cfg := config.Default()
	cfg.SignoffPaths = []string{"internal/state/**"}
	c := func(p string, a, d int) Change { return Change{Path: p, Added: a, Deleted: d} }
	tests := []struct {
		name      string
		changes   []Change
		candidate bool
		want      string
	}{
		{"empty", nil, false, Standard},
		{"readme", []Change{c("README.md", 1, 0)}, false, Docs},
		{"docs tree", []Change{c("docs/a/b.png", 1, 0), c("CHANGELOG", 1, 0)}, false, Docs},
		{"changelog variant", []Change{c("CHANGELOG-2.txt", 1, 0)}, false, Docs},
		{"docs plus code", []Change{c("README.md", 1, 0), c("main.go", 1, 0)}, false, Standard},
		{"code", []Change{c("main.go", 5, 5)}, false, Standard},
		{"gate.yaml", []Change{c("gate.yaml", 1, 0)}, false, Signoff},
		{"prek config", []Change{c(".pre-commit-config.yaml", 1, 0)}, false, Signoff},
		{"prek yml", []Change{c(".pre-commit-config.yml", 1, 0)}, false, Signoff},
		{"custom golangci", []Change{c(".golangci-custom.toml", 1, 0)}, false, Signoff},
		{"binary lock unknown removals", []Change{{Path: "bun.lockb", Binary: true}}, false, Signoff},
		{"prek toml", []Change{c("prek.toml", 1, 0)}, false, Signoff},
		{"golangci", []Change{c(".golangci.yml", 1, 0)}, false, Signoff},
		{"tsconfig nested", []Change{c("web/tsconfig.base.json", 1, 0)}, false, Signoff},
		{"workflow", []Change{c(".github/workflows/ci.yml", 1, 0)}, false, Signoff},
		{"coverage config", []Change{c(".testcoverage.yml", 1, 0)}, false, Signoff},
		{"lock additions only", []Change{c("go.sum", 3, 0)}, false, Standard},
		{"lock removals", []Change{c("go.sum", 3, 1)}, false, Signoff},
		{"signoff path", []Change{c("internal/state/x.go", 1, 0)}, false, Signoff},
		{"other internal", []Change{c("internal/lane/x.go", 1, 0)}, false, Standard},
		{"at line limit", []Change{c("a.go", 300, 100)}, false, Standard},
		{"over line limit", []Change{c("a.go", 300, 101)}, false, Signoff},
		{"docs over limit", []Change{c("a.md", 401, 0)}, false, Signoff},
		{"candidate config", []Change{c("a.go", 1, 0)}, true, Signoff},
		{"candidate config docs", []Change{c("a.md", 1, 0)}, true, Signoff},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.changes, cfg, tc.candidate)
			if got.Lane != tc.want {
				t.Fatalf("lane=%s want %s (%v)", got.Lane, tc.want, got.Reasons)
			}
			if (got.Lane == Signoff) != (len(got.Reasons) > 0) {
				t.Fatalf("reasons %v inconsistent with lane %s", got.Reasons, got.Lane)
			}
		})
	}
}
