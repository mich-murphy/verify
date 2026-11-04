package eligible

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/state"
)

const otherSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func findingsGen(allAccepted bool) *rapid.Generator[[]state.Finding] {
	one := rapid.Custom(func(t *rapid.T) state.Finding {
		f := state.Finding{
			Source:   rapid.SampledFrom([]string{state.SourceCheck, state.SourceGuard, state.SourceSetup, state.SourceReview}).Draw(t, "source"),
			Severity: rapid.SampledFrom([]string{state.SeverityBlock, state.SeverityNote}).Draw(t, "severity"),
			State:    rapid.SampledFrom([]string{state.StateOpen, state.StateResolved, state.StateAccepted}).Draw(t, "state"),
			Message:  "m",
		}
		if allAccepted {
			f.State = state.StateAccepted
		}
		return f
	})
	return rapid.Custom(func(t *rapid.T) []state.Finding {
		fs := rapid.SliceOfN(one, 0, 5).Draw(t, "findings")
		for i := range fs {
			fs[i].ID = fmt.Sprintf("F%d", i+1)
			fs[i].Rule = fs[i].ID
		}
		return fs
	})
}

func decisionsGen(ids []string, shas []string, kinds []string) *rapid.Generator[[]state.Decision] {
	return rapid.SliceOfN(rapid.Custom(func(t *rapid.T) state.Decision {
		d := state.Decision{
			Kind: rapid.SampledFrom(kinds).Draw(t, "kind"),
			SHA:  rapid.SampledFrom(shas).Draw(t, "sha"),
		}
		if d.Kind != state.DecideApprove {
			d.Target = rapid.SampledFrom(append([]string{"verify"}, ids...)).Draw(t, "target")
		}
		return d
	}), 0, 6)
}

type inputOpts struct {
	allAccepted bool
	kinds       []string
	shas        []string
}

func inputGen(o inputOpts) *rapid.Generator[Input] {
	if o.kinds == nil {
		o.kinds = []string{state.DecideApprove, state.DecideAccept, state.DecideWaive}
	}
	if o.shas == nil {
		o.shas = []string{sha, otherSHA}
	}
	return rapid.Custom(func(t *rapid.T) Input {
		fs := findingsGen(o.allAccepted).Draw(t, "findings")
		var ids []string
		var checkFs, reviewFs []state.Finding
		for _, f := range fs {
			ids = append(ids, f.ID)
			if f.Source == state.SourceReview {
				reviewFs = append(reviewFs, f)
			} else {
				checkFs = append(checkFs, f)
			}
		}
		lane := rapid.SampledFrom([]string{"docs", "standard", "signoff"}).Draw(t, "lane")
		check := &state.CheckRecord{
			SHA: sha, MergeBase: sha, Lane: lane,
			Status:   rapid.SampledFrom([]string{state.StatusPass, state.StatusFail}).Draw(t, "checkStatus"),
			Findings: checkFs,
		}
		if rapid.Bool().Draw(t, "failedStep") {
			check.Steps = []state.Step{{Name: "lint", Status: state.StatusFail}}
		}
		br := &state.Branch{
			Name:      "feature",
			Decisions: decisionsGen(ids, o.shas, o.kinds).Draw(t, "decisions"),
		}
		var rev *state.ReviewRecord
		if rapid.Bool().Draw(t, "hasReview") {
			rev = &state.ReviewRecord{SHA: sha, MergeBase: sha, Branch: "feature", Status: state.StatusPass, Findings: reviewFs}
			if rapid.Bool().Draw(t, "branchMirrorsReview") {
				br.Findings = slices.Clone(reviewFs)
			}
		}
		return Input{SHA: sha, MergeBase: sha, Lane: lane, Check: check, Review: rev, Branch: br, Config: config.Default()}
	})
}

func TestProp_EligibleIffNoMissingGaps(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := inputGen(inputOpts{}).Draw(t, "input")
		got := Evaluate(in)
		if got.Eligible != (len(got.Missing) == 0) {
			t.Fatalf("eligible=%v missing=%q", got.Eligible, got.Missing)
		}
		if got.Eligible && got.NextAction != "run verify ship" {
			t.Fatalf("eligible but next action %q", got.NextAction)
		}
	})
}

func TestProp_AcceptDecisionForDifferentSHANeverChangesVerdict(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := inputGen(inputOpts{}).Draw(t, "input")
		before := Evaluate(in)
		target := rapid.SampledFrom(append([]string{"F1", "F2", "F3"}, "verify")).Draw(t, "target")
		br := *in.Branch
		br.Decisions = append(slices.Clone(br.Decisions), state.Decision{Kind: state.DecideAccept, Target: target, SHA: otherSHA})
		in.Branch = &br
		if after := Evaluate(in); !reflect.DeepEqual(before, after) {
			t.Fatalf("accept %s on %s changed verdict\nbefore %+v\nafter  %+v", target, otherSHA, before, after)
		}
	})
}

func TestProp_AcceptedFindingsNeverBlock(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := inputGen(inputOpts{allAccepted: true}).Draw(t, "input")
		if got := Evaluate(in); len(got.Blocking) != 0 {
			t.Fatalf("accepted findings still block: %+v", got.Blocking)
		}
	})
}

func TestProp_SignoffLaneIsNotEligibleWithoutApproveOnTheSHA(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := inputGen(inputOpts{}).Draw(t, "input")
		if in.Check.Lane != "signoff" {
			return
		}
		for _, d := range in.Branch.Decisions {
			if d.Kind == state.DecideApprove && d.SHA == sha {
				return
			}
		}
		if got := Evaluate(in); got.Eligible {
			t.Fatalf("signoff eligible with decisions %+v", in.Branch.Decisions)
		}
	})
}
