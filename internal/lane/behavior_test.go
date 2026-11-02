package lane

import (
	"testing"

	"github.com/mich-murphy/verify/internal/config"
)

func TestDocsBehaviorCorrection(t *testing.T) {
	for _, tc := range []struct {
		name               string
		change             Change
		surfaces, critical []string
		want               string
	}{
		{name: "shell without executable bit", change: Change{Path: "docs/examples/run.sh", OldMode: "100644", NewMode: "100644"}, want: Standard},
		{name: "Go source", change: Change{Path: "docs/example.go"}, want: Standard},
		{name: "TypeScript source", change: Change{Path: "docs/sample.ts"}, want: Standard},
		{name: "compiled MDX documentation", change: Change{Path: "docs/sample.mdx"}, want: Standard},
		{name: "executable notebook", change: Change{Path: "docs/sample.ipynb"}, want: Standard},
		{name: "build input", change: Change{Path: "docs/Makefile"}, want: Standard},
		{name: "deploy input", change: Change{Path: "docs/compose.yaml"}, want: Standard},
		{name: "test input", change: Change{Path: "docs/check.feature"}, want: Standard},
		{name: "manifest", change: Change{Path: "docs/package.json"}, want: Standard},
		{name: "scenario markdown", change: Change{Path: "docs/example.md"}, surfaces: []string{"docs/**"}, want: Standard},
		{name: "scenario glob unchanged", change: Change{Path: "docs/nested/example.md"}, surfaces: []string{"docs/*.md"}, want: Docs},

		{name: "critical source", change: Change{Path: "docs/example.go"}, critical: []string{"docs/**"}, want: Signoff},
		{name: "plain prose including quoted code", change: Change{Path: "docs/tutorial.md", Added: 20, OldMode: "100644", NewMode: "100644"}, want: Docs},
		{name: "nonexecutable PNG", change: Change{Path: "docs/chart.png", Binary: true, OldMode: "100644", NewMode: "100644"}, want: Docs},
		{name: "mode only executable added", change: Change{Path: "docs/tutorial.md", OldMode: "100644", NewMode: "100755"}, want: Standard},
		{name: "mode only executable removed", change: Change{Path: "docs/tutorial.md", OldMode: "100755", NewMode: "100644"}, want: Standard},
		{name: "old executable deleted", change: Change{Path: "docs/tool", OldMode: "100755", NewMode: "000000"}, want: Standard},
		{name: "new executable", change: Change{Path: "docs/tool", OldMode: "000000", NewMode: "100755"}, want: Standard},
		{name: "unchanged executable", change: Change{Path: "docs/tool", OldMode: "100755", NewMode: "100755"}, want: Standard},
		{name: "symlink", change: Change{Path: "docs/link.md", OldMode: "000000", NewMode: "120000"}, want: Standard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Verify.Surfaces, cfg.SignoffPaths = tc.surfaces, tc.critical
			got := Compute([]Change{tc.change}, cfg, false)
			if got.Lane != tc.want {
				t.Fatalf("lane=%s want %s", got.Lane, tc.want)
			}
		})
	}
}
