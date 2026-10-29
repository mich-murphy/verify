// Package eligible decides whether a commit may ship (SPEC §3, §5–§8).
// It is pure: callers load the records and pass them in.
package eligible

import (
	"fmt"
	"slices"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/glob"
	"github.com/mich-murphy/verify/internal/state"
)

// Lane names, matching internal/lane (not imported to keep this package leaf-like).
const (
	laneDocs    = "docs"
	laneSignoff = "signoff"
)

// Input is everything Evaluate looks at. Records may be nil when absent.
type Input struct {
	SHA          string
	MergeBase    string
	Lane         string
	Check        *state.CheckRecord
	Review       *state.ReviewRecord
	Verify       *state.VerifyRecord
	Branch       *state.Branch
	ChangedPaths []string
	Config       config.Config
}

// Result is the verdict. Missing lists each required step that is not done or
// not passing, with a reason. NextAction names the single next command.
type Result struct {
	Eligible   bool
	Lane       string
	Missing    []string
	Blocking   []state.Finding
	NextAction string
}

type gap struct{ msg, action string }

type evaluator struct {
	in        Input
	decisions []state.Decision // decisions on in.SHA
	res       Result
}

// Evaluate applies the eligibility rules to in.
func Evaluate(in Input) Result {
	if in.Branch == nil {
		in.Branch = &state.Branch{}
	}
	if in.Check == nil || in.Check.SHA != in.SHA || in.Check.MergeBase != in.MergeBase || in.Check.Lane != in.Lane {
		return Result{
			Missing:    []string{"check: no check record for this commit"},
			NextAction: "run gate check",
		}
	}
	e := &evaluator{in: in}
	e.res.Lane = in.Check.Lane
	for _, d := range in.Branch.Decisions {
		if d.SHA == in.SHA {
			e.decisions = append(e.decisions, d)
		}
	}
	gaps := e.checkGaps()
	if in.Check.Lane != laneDocs {
		gaps = append(gaps, e.reviewGaps()...)
		gaps = append(gaps, e.verifyGaps()...)
	}
	gaps = append(gaps, e.approvalGaps()...)

	e.res.Eligible = len(gaps) == 0
	e.res.NextAction = "run gate ship"
	if len(gaps) > 0 {
		e.res.NextAction = gaps[0].action
	}
	for _, g := range gaps {
		e.res.Missing = append(e.res.Missing, g.msg)
	}
	return e.res
}

func (e *evaluator) has(kind, target string) bool {
	return slices.ContainsFunc(e.decisions, func(d state.Decision) bool {
		return d.Kind == kind && d.Target == target
	})
}

func (e *evaluator) approveAny() bool {
	return slices.ContainsFunc(e.decisions, func(d state.Decision) bool { return d.Kind == state.DecideApprove })
}

// CheckAssessment is the decision-aware check verdict shared with review admission.
type CheckAssessment struct {
	Passing  bool
	Blocking []state.Finding
}

// AssessCheck preserves raw execution failures while allowing explicit guard/setup decisions.
func AssessCheck(chk *state.CheckRecord, decisions []state.Decision, sha string) CheckAssessment {
	out := CheckAssessment{Passing: chk != nil}
	if chk == nil {
		return out
	}
	covered := func(f state.Finding) bool {
		kind := ""
		switch f.Source {
		case state.SourceGuard:
			kind = state.DecideAccept
		case state.SourceSetup:
			kind = state.DecideWaive
		default:
			return false
		}
		return slices.ContainsFunc(decisions, func(d state.Decision) bool { return d.SHA == sha && d.Kind == kind && d.Target == f.ID })
	}
	for _, f := range chk.Findings {
		if f.Blocking() && !covered(f) {
			out.Blocking = append(out.Blocking, f)
			out.Passing = false
		}
	}
	for _, s := range chk.Steps {
		if s.Status == state.StatusFail {
			out.Passing = false
		}
	}
	// A raw failure without a recorded blocking finding or failed step is untrusted.
	if chk.Status == state.StatusFail && len(chk.Findings) == 0 && len(chk.Steps) == 0 {
		out.Passing = false
	}
	if chk.Status != state.StatusPass && chk.Status != state.StatusFail {
		out.Passing = false
	}
	return out
}

// covered reports whether a decision on this SHA covers the check finding.
// check:<step> findings can never be covered.
func (e *evaluator) covered(f state.Finding) bool {
	switch f.Source {
	case state.SourceGuard, state.SourceReview:
		return e.has(state.DecideAccept, f.ID)
	case state.SourceSetup:
		return e.has(state.DecideWaive, f.ID)
	}
	return false
}

func (e *evaluator) checkGaps() []gap {
	var gaps []gap
	assessment := AssessCheck(e.in.Check, e.in.Branch.Decisions, e.in.SHA)
	for _, f := range assessment.Blocking {
		e.res.Blocking = append(e.res.Blocking, f)
		gaps = append(gaps, gap{
			msg:    fmt.Sprintf("check: blocking finding %s: %s", f.ID, f.Message),
			action: checkAction(f),
		})
	}
	if !assessment.Passing && len(gaps) == 0 {
		gaps = append(gaps, gap{"check: repository execution failed", "fix the checks, commit and run gate check"})
	}
	return gaps
}

func checkAction(f state.Finding) string {
	switch f.Source {
	case state.SourceGuard:
		return fmt.Sprintf("fix %s, commit and run gate check, or run gate decide accept %s --reason TEXT", f.ID, f.ID)
	case state.SourceSetup:
		return fmt.Sprintf("fix the setup, commit and run gate check, or run gate decide waive %s --reason TEXT", f.ID)
	}
	return fmt.Sprintf("fix %s, commit and run gate check", f.ID)
}

// Findings projects current branch dispositions over immutable review evidence.
// A missing branch entry cannot erase a finding in the exact-SHA review record.
func Findings(chk *state.CheckRecord, rev *state.ReviewRecord, ver *state.VerifyRecord, br *state.Branch) []state.Finding {
	var out []state.Finding
	if chk != nil {
		out = append(out, chk.Findings...)
	}
	if ver != nil {
		out = append(out, ver.Findings...)
	}
	known := map[string]bool{}
	if br != nil {
		for _, f := range br.Findings {
			known[f.ID] = true
			out = append(out, f)
		}
	}
	if rev != nil {
		for _, f := range rev.Findings {
			if !known[f.ID] {
				out = append(out, f)
			}
		}
	}
	return out
}

func (e *evaluator) reviewBlocking() []state.Finding {
	var out []state.Finding
	rev := e.in.Review
	if rev != nil && (rev.SHA != e.in.SHA || rev.MergeBase != e.in.MergeBase || rev.Branch != e.in.Branch.Name) {
		rev = nil
	}
	for _, f := range Findings(nil, rev, nil, e.in.Branch) {
		if f.Source == state.SourceReview && f.Blocking() && !e.covered(f) {
			out = append(out, f)
		}
	}
	return out
}

func (e *evaluator) reviewGaps() []gap {
	var gaps []gap
	// A parked branch with an approval may skip the review record once every
	// blocking review finding has been accepted.
	parkedOK := e.in.Branch.Parked && e.approveAny()
	if (e.in.Review == nil || e.in.Review.SHA != e.in.SHA || e.in.Review.MergeBase != e.in.MergeBase || e.in.Review.Branch != e.in.Branch.Name) && !parkedOK {
		gaps = append(gaps, gap{"review: no review record for this commit", "run gate review"})
	}
	for _, f := range e.reviewBlocking() {
		e.res.Blocking = append(e.res.Blocking, f)
		action := fmt.Sprintf("fix %s, commit and run gate check", f.ID)
		if e.in.Branch.Parked {
			action = fmt.Sprintf("run gate decide accept %s --reason TEXT", f.ID)
		}
		gaps = append(gaps, gap{fmt.Sprintf("review: blocking finding %s: %s", f.ID, f.Message), action})
	}
	return gaps
}

func (e *evaluator) verifyRequired() bool {
	for _, p := range e.in.ChangedPaths {
		if glob.MatchAny(e.in.Config.Verify.Surfaces, p) {
			return true
		}
	}
	return false
}

func (e *evaluator) verifyGaps() []gap {
	if !e.verifyRequired() {
		return nil
	}
	v := e.in.Verify
	if v == nil || v.SHA != e.in.SHA || v.MergeBase != e.in.MergeBase {
		return []gap{{"verify: a declared surface changed and no verify record exists", "run gate verify"}}
	}
	switch v.Verdict {
	case "go":
		return nil
	case "no-surface":
		return []gap{{"verify: surface changed but record says no-surface", "run gate verify"}}
	case "no-go":
		return []gap{{"verify: verdict no-go", "fix the failing scenarios, commit and run gate check"}}
	case "inconclusive":
		if e.has(state.DecideWaive, "verify") {
			return nil
		}
		return []gap{{"verify: verdict inconclusive", "fix the verify setup and run gate verify, or run gate decide waive verify --reason TEXT"}}
	}
	return []gap{{fmt.Sprintf("verify: unknown verdict %q", v.Verdict), "run gate verify"}}
}

func (e *evaluator) approvalGaps() []gap {
	if e.approveAny() {
		return nil
	}
	const act = "run gate decide approve --reason TEXT"
	switch {
	case e.in.Branch.Parked:
		return []gap{{"branch is parked: approve is required", act}}
	case e.in.Check.Lane == laneSignoff:
		return []gap{{"signoff: no approve decision for this commit", act}}
	}
	return nil
}
