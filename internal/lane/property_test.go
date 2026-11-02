package lane

import (
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/mich-murphy/verify/internal/config"
)

// Plain names cannot match a quality-config prefix such as "coverage" or "tsconfig".
func plainName() *rapid.Generator[string] {
	return rapid.StringMatching(`z[a-y0-9]{0,6}`)
}

func docsPath() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Map(plainName(), func(n string) string { return "docs/" + n + ".md" }),
		rapid.Map(plainName(), func(n string) string { return n + ".md" }),
		rapid.Just("README.md"),
		rapid.Just("CHANGELOG"),
	)
}

func codePath() *rapid.Generator[string] {
	return rapid.Map(rapid.Custom(func(t *rapid.T) [2]string {
		return [2]string{
			rapid.SampledFrom([]string{"", "internal/", "src/web/", "cmd/x/"}).Draw(t, "dir"),
			plainName().Draw(t, "name"),
		}
	}), func(p [2]string) string { return p[0] + p[1] + ".go" })
}

func anyPath() *rapid.Generator[string] {
	return rapid.OneOf(docsPath(), codePath(),
		rapid.SampledFrom([]string{
			"go.mod", "go.sum", "package.json", "verify.yaml", ".golangci.yml",
			".github/workflows/ci.yml", "tsconfig.json", "coverage.md", "web/yarn.lock",
		}))
}

func change(paths *rapid.Generator[string]) *rapid.Generator[Change] {
	return rapid.Custom(func(t *rapid.T) Change {
		return Change{
			Path:    paths.Draw(t, "path"),
			Added:   rapid.IntRange(0, 120).Draw(t, "added"),
			Deleted: rapid.IntRange(0, 120).Draw(t, "deleted"),
			Binary:  rapid.Bool().Draw(t, "binary"),
		}
	})
}

func smallDocs(t *rapid.T) []Change {
	cs := rapid.SliceOfN(change(docsPath()), 1, 4).Draw(t, "docs")
	for i := range cs {
		cs[i].Added, cs[i].Deleted = cs[i].Added%20, cs[i].Deleted%20
	}
	return cs
}

func TestProp_SmallDocsOnlyChangeIsDocsLane(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		if got := Compute(smallDocs(t), config.Default(), false); got.Lane != Docs {
			t.Fatalf("lane %q reasons %v", got.Lane, got.Reasons)
		}
	})
}

func TestProp_DocsLaneImpliesEveryPathIsDocs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cs := rapid.SliceOf(change(anyPath())).Draw(t, "changes")
		if Compute(cs, config.Default(), false).Lane != Docs {
			return
		}
		for _, c := range cs {
			if !IsDocs(c.Path) {
				t.Fatalf("docs lane with non-docs path %q", c.Path)
			}
		}
	})
}

func TestProp_AddingNonDocsChangeToDocsSetNeverYieldsDocs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cs := append(smallDocs(t), change(codePath()).Draw(t, "code"))
		if got := Compute(cs, config.Default(), false); got.Lane == Docs {
			t.Fatalf("docs lane for %+v", cs)
		}
	})
}

func TestProp_AnySignoffReasonImpliesSignoffLane(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cs := rapid.SliceOf(change(anyPath())).Draw(t, "changes")
		got := Compute(cs, config.Default(), rapid.Bool().Draw(t, "candidate"))
		if (len(got.Reasons) > 0) != (got.Lane == Signoff) {
			t.Fatalf("lane %q with reasons %v", got.Lane, got.Reasons)
		}
	})
}

func TestProp_DiffOver400LinesIsSignoffAndAtMost400IsNot(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		total := rapid.OneOf(rapid.IntRange(0, 800), rapid.IntRange(398, 402)).Draw(t, "total")
		n := rapid.IntRange(1, 4).Draw(t, "files")
		cs := make([]Change, n)
		for i := range cs {
			cs[i] = Change{Path: codePath().Draw(t, "path"), Added: total / n}
		}
		cs[0].Added += total - (total/n)*n
		got := Compute(cs, config.Default(), false)
		if (got.Lane == Signoff) != (total > 400) {
			t.Fatalf("%d lines gave lane %q reasons %v", total, got.Lane, got.Reasons)
		}
	})
}

func TestProp_QualityConfigChangeAlwaysSignoff(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cs := rapid.SliceOfN(change(anyPath()), 0, 3).Draw(t, "changes")
		q := rapid.SampledFrom([]string{"verify.yaml", ".golangci.yml", ".github/workflows/ci.yml", "web/tsconfig.json"}).Draw(t, "quality")
		cs = slices.Insert(cs, rapid.IntRange(0, len(cs)).Draw(t, "at"), Change{Path: q})
		if got := Compute(cs, config.Default(), false); got.Lane != Signoff {
			t.Fatalf("lane %q for %+v", got.Lane, cs)
		}
	})
}
