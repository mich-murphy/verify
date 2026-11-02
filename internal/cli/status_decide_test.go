package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/state"
)

func TestStatusExitCodes(t *testing.T) {
	e := newShipEnv(t)
	r, code := runCLI(t, "status")
	if code != ExitBlocked || r.NextAction != "run verify check" {
		t.Fatalf("missing check: code %d %+v", code, r)
	}
	e.records(t, "standard", false)
	if r, code = runCLI(t, "status"); code != ExitBlocked || r.NextAction != "run verify review" {
		t.Fatalf("missing review: code %d %+v", code, r)
	}
	e.records(t, "standard", true)
	if r, code = runCLI(t, "status"); code != ExitOK || r.NextAction != "run verify ship" {
		t.Fatalf("eligible: code %d %+v", code, r)
	}
	if err := e.st.UpdateBranch(t.Context(), "feature", func(b *state.Branch) error {
		b.Findings = append(b.Findings, state.Finding{ID: "F1", Source: "review", Severity: "block", State: "open", Message: "bug"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r, code = runCLI(t, "status"); code != ExitFindings || r.Status != "blocked-by-findings" {
		t.Fatalf("blocking finding: code %d %+v", code, r)
	}
}

func TestStatusShowsWaivedAndAccepted(t *testing.T) {
	e := newShipEnv(t)
	base := strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "main"))
	now := time.Now().UTC()
	if err := e.st.SaveCheck(t.Context(), &state.CheckRecord{SHA: e.sha, MergeBase: base, Lane: "standard", Scope: "affected", Status: "fail", Started: now, Finished: now, Findings: []state.Finding{
		{ID: "setup:x", Source: "setup", Severity: "block", State: "open", Message: "missing setup"},
		{ID: "guard:g", Source: "guard", Severity: "block", State: "open", Message: "guard hit"},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"waive", "x"}, {"accept", "guard:g"}} {
		if r, code := runCLI(t, append([]string{"decide"}, append(args, "--reason", "r")...)...); code != ExitOK {
			t.Fatalf("%v: code %d: %+v", args, code, r)
		}
	}
	s, _ := runCLI(t, "status")
	got := map[string]string{}
	for _, f := range s.Findings {
		got[f.ID] = f.State
	}
	if got["setup:x"] != "waived" || got["guard:g"] != "accepted" {
		t.Fatalf("states %v", got)
	}
}

func TestDecideApproveRecordsCoverage(t *testing.T) {
	e := newShipEnv(t)
	base := strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "main"))
	if r, code := runCLI(t, "decide", "approve", "--reason", "ok"); code != ExitBlocked || r.Message != "run verify check first" {
		t.Fatalf("approve without check: code %d %+v", code, r)
	}
	e.records(t, "standard", true)
	if r, code := runCLI(t, "decide", "approve", "--reason", "ok"); code != ExitOK {
		t.Fatalf("approve: code %d %+v", code, r)
	}
	want := state.DecisionCoverage{From: base, To: e.sha, Files: 1, Lines: 2, Check: e.sha}
	assertLastCoverage(t, e, want)

	first := e.sha
	commit(t, e.repo, "b.go", "package b\n\nvar B = 1\nvar C = 2\n")
	e.sha = strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "HEAD"))
	if err := e.st.UpdateBranch(t.Context(), "feature", func(b *state.Branch) error { b.LastReviewedSHA = first; return nil }); err != nil {
		t.Fatal(err)
	}
	e.records(t, "standard", true)
	if r, code := runCLI(t, "decide", "approve", "--reason", "ok"); code != ExitOK {
		t.Fatalf("approve: code %d %+v", code, r)
	}
	assertLastCoverage(t, e, state.DecisionCoverage{From: first, To: e.sha, Files: 1, Lines: 4, Check: e.sha})
}

func assertLastCoverage(t *testing.T, e *shipEnv, want state.DecisionCoverage) {
	t.Helper()
	b, err := state.Open(filepath.Join(e.repo, ".git")).LoadBranch("feature")
	if err != nil || len(b.Decisions) == 0 {
		t.Fatalf("branch %+v err %v", b, err)
	}
	got := b.Decisions[len(b.Decisions)-1].Coverage
	if got == nil || *got != want {
		t.Fatalf("coverage %+v want %+v", got, want)
	}
}
