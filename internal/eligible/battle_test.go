package eligible

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/state"
)

// Both records pass their individual persistence validators. A same-ID branch
// entry must not be able to downgrade an immutable review's severity.
func TestBattlePersistedFindingIdentityCannotDowngrade(t *testing.T) {
	st := state.Open(t.TempDir())
	f := blk("F1", state.SourceReview)
	f.Rule, f.Path = "correctness", "a.go"
	rec := &state.ReviewRecord{SHA: sha, MergeBase: sha, Branch: "feature", Reviewer: "codex", Status: state.StatusFail, Findings: []state.Finding{f}, CallsUsed: 1, Attempts: 1, Finished: time.Now().UTC()}
	if err := st.SaveReview(rec); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateBranch("feature", func(b *state.Branch) error {
		changed := f
		changed.Severity, changed.Message = state.SeverityNote, "different claim"
		b.Findings = []state.Finding{changed}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadReview(sha)
	if err != nil {
		t.Fatal(err)
	}
	br, err := st.LoadBranch("feature")
	if err != nil {
		t.Fatal(err)
	}
	got := Evaluate(Input{SHA: sha, MergeBase: sha, Lane: "standard", Check: chk("standard"), Review: loaded, Branch: br})
	if got.Eligible || !strings.Contains(strings.Join(got.Missing, " "), "conflicting immutable identity") {
		t.Fatalf("inconsistent same-ID finding erased immutable blocker: %+v", got)
	}
	br.Parked = true
	br.Decisions = []state.Decision{dec(state.DecideAccept, "F1"), dec(state.DecideApprove, "")}
	got = Evaluate(Input{SHA: sha, MergeBase: sha, Lane: "standard", Check: chk("standard"), Review: loaded, Branch: br})
	if got.Eligible || !strings.Contains(strings.Join(got.Missing, " "), "conflicting immutable identity") {
		t.Fatalf("acceptance and parked approval bypassed inconsistent identity: %+v", got)
	}
}

func BenchmarkBattleEvaluateDecisions(b *testing.B) {
	for _, n := range []int{1, 100, 1000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			in := Input{SHA: sha, MergeBase: sha, Lane: "docs", Check: chk("docs"), Branch: &state.Branch{}}
			for range n {
				in.Branch.Decisions = append(in.Branch.Decisions, dec(state.DecideApprove, ""))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if !Evaluate(in).Eligible {
					b.Fatal("unexpected gap")
				}
			}
		})
	}
}

func TestBattleProjectionKeepsRawFactsAndCoherentDispositions(t *testing.T) {
	f := blk("F1", state.SourceReview)
	f.Repro = &state.Repro{Input: "input", Expected: "expected"}
	for _, disposition := range []string{state.StateAccepted, state.StateResolved} {
		branchFinding := f
		branchFinding.State = disposition
		record := &state.ReviewRecord{Findings: []state.Finding{f}}
		branch := &state.Branch{Findings: []state.Finding{branchFinding}}
		got := Findings(nil, record, nil, branch)
		if len(got) != 1 || got[0].State != disposition || got[0].Message != f.Message {
			t.Fatalf("bad projection: %+v", got)
		}
		if record.Findings[0].State != state.StateOpen {
			t.Fatal("raw evidence mutated")
		}
		branch.Findings[0].Repro = &state.Repro{Input: "different", Expected: "expected"}
		if findingConflict(record, branch) == nil {
			t.Fatal("conflicting repro accepted")
		}
		if got := Findings(nil, record, nil, branch); !got[0].Blocking() {
			t.Fatal("conflicting accepted disposition erased raw blocker")
		}
	}
}

func TestBattleEffectiveFindingsDecisionScopeAndImmutability(t *testing.T) {
	for _, source := range []string{state.SourceGuard, state.SourceSetup, state.SourceCheck} {
		f := blk("finding", source)
		raw := &state.CheckRecord{Findings: []state.Finding{f}}
		kind := state.DecideAccept
		if source == state.SourceSetup {
			kind = state.DecideWaive
		}
		br := &state.Branch{Decisions: []state.Decision{dec(kind, f.ID)}}
		got := EffectiveFindings(raw, nil, nil, br, sha)
		if (got[0].State == state.StateAccepted) != (source != state.SourceCheck) {
			t.Fatalf("source %s: %+v", source, got)
		}
		if raw.Findings[0].State != state.StateOpen {
			t.Fatal("raw evidence mutated")
		}
		br.Decisions[0].SHA = strings.Repeat("b", 40)
		if got := EffectiveFindings(raw, nil, nil, br, sha); got[0].State != state.StateOpen {
			t.Fatal("stale decision applied")
		}
	}
}
