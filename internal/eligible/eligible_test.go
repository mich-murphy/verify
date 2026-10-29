package eligible

import (
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/state"
)

const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func chk(lane string, fs ...state.Finding) *state.CheckRecord {
	return &state.CheckRecord{SHA: sha, MergeBase: sha, Lane: lane, Status: "pass", Findings: fs}
}

func rev() *state.ReviewRecord {
	return &state.ReviewRecord{SHA: sha, MergeBase: sha, Branch: "feature", Status: "pass"}
}

func blk(id, src string) state.Finding {
	return state.Finding{ID: id, Source: src, Rule: id, Severity: state.SeverityBlock, State: state.StateOpen, Message: "m"}
}

func dec(kind, target string) state.Decision {
	return state.Decision{Kind: kind, Target: target, SHA: sha}
}

func TestReviewProjectionAndFreshness(t *testing.T) {
	f := blk("F1", "review")
	rev := &state.ReviewRecord{SHA: sha, MergeBase: sha, Branch: "feature", Status: "fail", Findings: []state.Finding{f}}
	check := chk("standard")
	input := Input{SHA: sha, MergeBase: sha, Lane: "standard", Check: check, Review: rev, Branch: &state.Branch{Name: "feature"}}
	if got := Evaluate(input); got.Eligible || len(got.Blocking) != 1 {
		t.Fatalf("lost branch must preserve record blocker: %+v", got)
	}
	input.Branch.Findings = []state.Finding{{ID: "F2", Source: "review", Severity: "note", State: "open", Message: "other"}}
	if got := Evaluate(input); got.Eligible || len(got.Blocking) != 1 {
		t.Fatalf("mismatched branch IDs must preserve record blocker: %+v", got)
	}
	input.Branch.Findings = append(input.Branch.Findings, state.Finding{ID: "F1", Source: "review", Severity: "block", State: "accepted", Message: "accepted"})
	if got := Evaluate(input); !got.Eligible {
		t.Fatalf("accepted branch disposition should override stale record: %+v", got)
	}
	input.Branch.Name = "elsewhere"
	if got := Evaluate(input); got.Eligible {
		t.Fatalf("other branch cannot reuse review: %+v", got)
	}
	input.Branch.Name = "feature"
	input.MergeBase = strings.Repeat("b", 40)
	if got := Evaluate(input); got.Eligible {
		t.Fatalf("base movement cannot reuse check: %+v", got)
	}
}

func TestEvaluate(t *testing.T) {
	surf := config.Default()
	surf.Verify.Surfaces = []string{"cmd/**"}
	cmdPath := []string{"cmd/gate/main.go"}
	other := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name     string
		in       Input
		eligible bool
		action   string // substring of NextAction
		missing  string // substring of Missing joined
		blocking int
	}{
		{"no check", Input{SHA: sha}, false, "gate check", "no check record", 0},
		{"check for other sha", Input{SHA: other, Check: chk("standard")}, false, "gate check", "no check", 0},
		{"docs ok", Input{SHA: sha, Check: chk("docs")}, true, "gate ship", "", 0},
		{"docs needs no review", Input{SHA: sha, Check: chk("docs"), ChangedPaths: cmdPath, Config: surf}, true, "gate ship", "", 0},
		{"standard no review", Input{SHA: sha, Check: chk("standard")}, false, "gate review", "no review record", 0},
		{"standard review ok", Input{SHA: sha, Check: chk("standard"), Review: rev()}, true, "gate ship", "", 0},
		{"review for other sha", Input{SHA: sha, Check: chk("standard"), Review: &state.ReviewRecord{SHA: other}}, false, "gate review", "no review record", 0},
		{"check guard finding", Input{SHA: sha, Check: chk("standard", blk("guard:secrets", "guard")), Review: rev()}, false, "decide accept guard:secrets", "guard:secrets", 1},
		{"guard accepted", Input{
			SHA: sha, Check: chk("standard", blk("guard:secrets", "guard")), Review: rev(),
			Branch: &state.Branch{Decisions: []state.Decision{dec("accept", "guard:secrets")}},
		}, true, "gate ship", "", 0},
		{"accept on other sha ignored", Input{
			SHA: sha, Check: chk("standard", blk("guard:secrets", "guard")), Review: rev(),
			Branch: &state.Branch{Decisions: []state.Decision{{Kind: "accept", Target: "guard:secrets", SHA: other}}},
		}, false, "accept", "", 1},
		{"setup waived", Input{
			SHA: sha, Check: chk("docs", blk("setup:x", "setup")),
			Branch: &state.Branch{Decisions: []state.Decision{dec("waive", "setup:x")}},
		}, true, "gate ship", "", 0},
		{"setup not covered by accept", Input{
			SHA: sha, Check: chk("docs", blk("setup:x", "setup")),
			Branch: &state.Branch{Decisions: []state.Decision{dec("accept", "setup:x")}},
		}, false, "waive setup:x", "", 1},
		{"check step cannot be accepted", Input{
			SHA: sha, Check: chk("docs", blk("check:test", "check")),
			Branch: &state.Branch{Decisions: []state.Decision{dec("accept", "check:test"), dec("waive", "check:test")}},
		}, false, "fix check:test", "", 1},
		{"note finding ignored", Input{SHA: sha, Check: chk("docs", state.Finding{ID: "n", Severity: "note", State: "open"})}, true, "gate ship", "", 0},
		{"open review finding", Input{
			SHA: sha, Check: chk("standard"), Review: rev(),
			Branch: &state.Branch{Findings: []state.Finding{blk("F1", "review")}},
		}, false, "fix F1", "F1", 1},
		{"review finding resolved on branch", Input{
			SHA: sha, Check: chk("standard"),
			Review: &state.ReviewRecord{SHA: sha, Findings: []state.Finding{blk("F1", "review")}},
			Branch: &state.Branch{Findings: []state.Finding{{ID: "F1", Source: "review", Severity: "block", State: "resolved"}}},
		}, true, "gate ship", "", 0},
		{"record finding unknown to branch blocks", Input{
			SHA: sha, Check: chk("standard"),
			Review: &state.ReviewRecord{SHA: sha, Findings: []state.Finding{blk("F2", "review")}},
		}, false, "fix F2", "F2", 1},
		{"review finding covered by accept decision", Input{
			SHA: sha, Check: chk("standard"), Review: rev(),
			Branch: &state.Branch{Findings: []state.Finding{blk("F1", "review")}, Decisions: []state.Decision{dec("accept", "F1")}},
		}, true, "gate ship", "", 0},
		{"parked without approve", Input{SHA: sha, Check: chk("standard"), Review: rev(), Branch: &state.Branch{Parked: true}}, false, "approve", "parked", 0},
		{"parked docs without approve", Input{SHA: sha, Check: chk("docs"), Branch: &state.Branch{Parked: true}}, false, "approve", "parked", 0},
		{"parked with approve and no review record", Input{
			SHA: sha, Check: chk("standard"),
			Branch: &state.Branch{Parked: true, Decisions: []state.Decision{dec("approve", "")}},
		}, true, "gate ship", "", 0},
		{"parked with approve, finding still open", Input{
			SHA: sha, Check: chk("standard"),
			Branch: &state.Branch{
				Parked: true, Findings: []state.Finding{blk("F1", "review")},
				Decisions: []state.Decision{dec("approve", "")},
			},
		}, false, "decide accept F1", "F1", 1},
		{"parked with approve, finding accepted", Input{
			SHA: sha, Check: chk("standard"),
			Branch: &state.Branch{
				Parked: true, Findings: []state.Finding{{ID: "F1", Source: "review", Severity: "block", State: "accepted"}},
				Decisions: []state.Decision{dec("approve", ""), dec("accept", "F1")},
			},
		}, true, "gate ship", "", 0},
		{"signoff without approve", Input{SHA: sha, Check: chk("signoff"), Review: rev()}, false, "decide approve", "signoff", 0},
		{"signoff approved", Input{
			SHA: sha, Check: chk("signoff"), Review: rev(),
			Branch: &state.Branch{Decisions: []state.Decision{dec("approve", "")}},
		}, true, "gate ship", "", 0},
		{"signoff approve on other sha", Input{
			SHA: sha, Check: chk("signoff"), Review: rev(),
			Branch: &state.Branch{Decisions: []state.Decision{{Kind: "approve", SHA: other}}},
		}, false, "decide approve", "", 0},
		{"verify required, missing", Input{SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf}, false, "gate verify", "verify", 0},
		{"verify not required for other paths", Input{SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: []string{"a.go"}, Config: surf}, true, "gate ship", "", 0},
		{"verify go", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: sha, Verdict: "go"},
		}, true, "gate ship", "", 0},
		{"verify no-surface", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: sha, Verdict: "no-surface"},
		}, false, "gate verify", "surface changed", 0},
		{"verify no-go", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: sha, Verdict: "no-go"},
		}, false, "fix", "no-go", 0},
		{"verify inconclusive", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: sha, Verdict: "inconclusive"},
		}, false, "waive verify", "inconclusive", 0},
		{"verify unknown verdict", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: sha, Verdict: "??"},
		}, false, "gate verify", "unknown verdict", 0},
		{"verify record for other sha", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: other, Verdict: "go"},
		}, false, "gate verify", "no verify record", 0},
		{"verify no-go cannot be waived", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: sha, Verdict: "no-go"}, Branch: &state.Branch{Decisions: []state.Decision{dec("waive", "verify")}},
		}, false, "fix", "no-go", 0},
		{"verify waived", Input{
			SHA: sha, Check: chk("standard"), Review: rev(), ChangedPaths: cmdPath, Config: surf,
			Verify: &state.VerifyRecord{SHA: sha, Verdict: "inconclusive"},
			Branch: &state.Branch{Decisions: []state.Decision{dec("waive", "verify")}},
		}, true, "gate ship", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.in.Check != nil {
				tc.in.MergeBase = sha
				tc.in.Lane = tc.in.Check.Lane
			}
			if tc.in.Branch == nil {
				tc.in.Branch = &state.Branch{Name: "feature"}
			} else {
				tc.in.Branch.Name = "feature"
			}
			if tc.in.Review != nil && tc.in.Review.SHA == sha {
				tc.in.Review.MergeBase = sha
				tc.in.Review.Branch = "feature"
			}
			if tc.in.Verify != nil && tc.in.Verify.SHA == sha {
				tc.in.Verify.MergeBase = sha
			}
			r := Evaluate(tc.in)
			if r.Eligible != tc.eligible {
				t.Fatalf("eligible=%v want %v: %+v", r.Eligible, tc.eligible, r)
			}
			if !strings.Contains(r.NextAction, tc.action) {
				t.Errorf("next action %q lacks %q", r.NextAction, tc.action)
			}
			if !strings.Contains(strings.Join(r.Missing, "\n"), tc.missing) {
				t.Errorf("missing %v lacks %q", r.Missing, tc.missing)
			}
			if len(r.Blocking) != tc.blocking {
				t.Errorf("blocking %d want %d: %+v", len(r.Blocking), tc.blocking, r.Blocking)
			}
			if tc.eligible && len(r.Missing) != 0 {
				t.Errorf("eligible with missing %v", r.Missing)
			}
		})
	}
}
