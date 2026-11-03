package review

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/mich-murphy/verify/internal/state"
)

var propPaths = []string{"a.go", "internal/b.go", "web/c.ts"}

type propFinding struct {
	Kind, Path, Claim string
	Line              int
	Repro             *state.Repro
}

func (p propFinding) wire() map[string]any {
	var repro any
	if p.Repro != nil {
		repro = map[string]string{"input": p.Repro.Input, "expected": p.Repro.Expected}
	}
	return map[string]any{"kind": p.Kind, "path": p.Path, "line": p.Line, "claim": p.Claim, "repro": repro}
}

func (p propFinding) want() state.Finding {
	f := state.Finding{
		Source: state.SourceReview, Rule: p.Kind, Severity: state.SeverityNote,
		Path: p.Path, Line: p.Line, Message: p.Claim, Repro: p.Repro, State: state.StateOpen,
	}
	if p.Kind == "correctness" && p.Repro != nil {
		f.Severity = state.SeverityBlock
	}
	return f
}

func text() *rapid.Generator[string] { return rapid.StringN(1, 40, 200) }

func findingGen(kinds *rapid.Generator[string], repro *rapid.Generator[*state.Repro]) *rapid.Generator[propFinding] {
	return rapid.Custom(func(t *rapid.T) propFinding {
		return propFinding{
			Kind:  kinds.Draw(t, "kind"),
			Path:  rapid.SampledFrom(propPaths).Draw(t, "path"),
			Line:  rapid.IntRange(0, 5000).Draw(t, "line"),
			Claim: text().Draw(t, "claim"),
			Repro: repro.Draw(t, "repro"),
		}
	})
}

func reproGen() *rapid.Generator[*state.Repro] {
	return rapid.OneOf(
		rapid.Just[*state.Repro](nil),
		rapid.Custom(func(t *rapid.T) *state.Repro {
			return &state.Repro{Input: text().Draw(t, "input"), Expected: text().Draw(t, "expected")}
		}),
	)
}

func marshalResponse(t *rapid.T, fs []propFinding, resolved []string) []byte {
	wire := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		wire = append(wire, f.wire())
	}
	raw, err := json.Marshal(map[string]any{"findings": wire, "resolved": append([]string{}, resolved...)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProp_ValidResponseRoundTripsInOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		fs := rapid.SliceOfN(findingGen(rapid.SampledFrom(Kinds), reproGen()), 0, 8).Draw(t, "findings")
		open := []string{"F1", "F2", "F3", "F4"}
		resolved := rapid.SliceOfNDistinct(rapid.SampledFrom(open), 0, 4, func(s string) string { return s }).Draw(t, "resolved")
		got, err := Parse(marshalResponse(t, fs, resolved), open, propPaths)
		if err != nil {
			t.Fatal(err)
		}
		var want []state.Finding
		for _, f := range fs {
			want = append(want, f.want())
		}
		if len(got.Findings) != len(want) {
			t.Fatalf("got %d findings, want %d", len(got.Findings), len(want))
		}
		for i := range want {
			if !equalFinding(got.Findings[i], want[i]) {
				t.Fatalf("finding %d: got %+v want %+v", i, got.Findings[i], want[i])
			}
		}
		if !slices.Equal(got.Resolved, resolved) {
			t.Fatalf("resolved %v want %v", got.Resolved, resolved)
		}
	})
}

func equalFinding(a, b state.Finding) bool {
	ra, rb := a.Repro, b.Repro
	a.Repro, b.Repro = nil, nil
	return a == b && (ra == nil) == (rb == nil) && (ra == nil || *ra == *rb)
}

func TestProp_CorrectnessFindingWithNullReproParsesAsNote(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := findingGen(rapid.Just("correctness"), rapid.Just[*state.Repro](nil)).Draw(t, "finding")
		got, err := Parse(marshalResponse(t, []propFinding{f}, nil), nil, propPaths)
		if err != nil {
			t.Fatal(err)
		}
		if g := got.Findings[0]; g.Severity != state.SeverityNote || g.Blocking() {
			t.Fatalf("null-repro correctness finding is %q: %+v", g.Severity, g)
		}
	})
}

func TestProp_UnknownKindIsRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.String().Filter(func(s string) bool { return !slices.Contains(Kinds, s) }).Draw(t, "kind")
		f := findingGen(rapid.Just(kind), reproGen()).Draw(t, "finding")
		if _, err := Parse(marshalResponse(t, []propFinding{f}, nil), nil, propPaths); !errors.Is(err, ErrInvalidOutput) {
			t.Fatalf("kind %q accepted, err %v", kind, err)
		}
	})
}

func TestProp_ResolvedIDOutsideOpenSetIsRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		open := rapid.SliceOfN(rapid.StringMatching(`F[0-9]{1,3}`), 0, 4).Draw(t, "open")
		stray := rapid.StringMatching(`G[0-9]{1,3}`).Draw(t, "stray")
		resolved := append(rapid.SliceOfNDistinct(rapid.SampledFrom(append([]string{"F1"}, open...)), 0, 3, func(s string) string { return s }).Draw(t, "inSet"), stray)
		if _, err := Parse(marshalResponse(t, nil, resolved), open, propPaths); !errors.Is(err, ErrInvalidOutput) {
			t.Fatalf("resolved %v accepted against open %v, err %v", resolved, open, err)
		}
	})
}
