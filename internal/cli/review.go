package cli

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/mich-murphy/verify/internal/check"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/eligible"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/lane"
	"github.com/mich-murphy/verify/internal/review"
	"github.com/mich-murphy/verify/internal/state"
)

func init() { register("review", runReview) }

// reviewPlan is what one review call needs, resolved before the reviewer runs.
type reviewPlan struct {
	packet    []byte
	openIDs   []string
	diffPaths []string
	delta     bool
}

func runReview(env *Env) int {
	fs := env.Flags("review")
	intentFile := fs.String("intent", "", "file with the change's intent")
	choice := fs.String("reviewer", "", "auto, claude or codex (default: $GATE_REVIEWER, else auto)")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("review", ExitUsage, "usage: gate review [--intent FILE] [--reviewer auto|claude|codex] [--json]", "fix the arguments")
	}
	blocked := func(msg, next string) int { return env.Fail("review", ExitBlocked, msg, next) }
	repo, err := gitx.Open(env.Ctx, env.Dir)
	if err != nil {
		return blocked("not a git repository: "+err.Error(), "run gate inside a git work tree")
	}
	sha, err := repo.Head(env.Ctx)
	if err != nil {
		return blocked(err.Error(), "make a commit first")
	}
	loaded, err := config.Load(env.Ctx, repo, sha)
	if err != nil {
		if errors.Is(err, config.ErrInvalid) {
			return env.Fail("review", ExitUsage, err.Error(), "fix gate.yaml")
		}
		return blocked(err.Error(), "make sure the base branch exists")
	}
	cfg := loaded.Config
	if *choice == "" {
		*choice = cmp.Or(os.Getenv("GATE_REVIEWER"), "auto")
	}
	rv, err := review.Select(*choice, os.Getenv("GATE_AUTHOR"), cmp.Or(os.Getenv("GATE_CLAUDE_MODEL"), "opus"), os.Getenv("GATE_CODEX_MODEL"))
	if err != nil {
		return env.Fail("review", ExitUsage, err.Error(), "set GATE_REVIEWER or --reviewer to auto, claude or codex")
	}
	changes, err := repo.Changes(env.Ctx, loaded.MergeBase, sha)
	if err != nil {
		return blocked(err.Error(), "resolve the git error")
	}
	l := lane.Compute(check.ToLane(changes), cfg, loaded.FromCandidate)
	if l.Lane == lane.Docs {
		return env.Emit(Result{Command: "review", SHA: sha, Lane: l.Lane, Status: "ok", Message: "review not required for the docs lane"}, ExitOK)
	}
	st := state.Open(repo.CommonDir)
	name, _ := repo.Branch(env.Ctx)
	if name == "" {
		return blocked("HEAD is detached; review requires a branch", "check out a branch")
	}
	// Serialize admission, call and publication for this branch, without blocking other branches.
	unlock, err := state.LockFile(st.BranchPath(name) + ".review.lock")
	if err != nil {
		return blocked(err.Error(), "resolve the state lock error")
	}
	defer unlock()
	br, msg, next := reviewGate(st, name, sha, loaded.MergeBase, l.Lane)
	if msg != "" {
		return blocked(msg, next)
	}
	previous, err := st.LoadReview(sha)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return blocked(err.Error(), "delete the corrupt review record and retry gate review")
	}
	if err == nil && previous.MergeBase == loaded.MergeBase && previous.Branch == name {
		return reuseReview(env, previous, br, l.Lane)
	}
	if msg, next = reviewBudget(st, name, br); msg != "" {
		return blocked(msg, next)
	}
	return runPlannedReview(env, repo, st, rv, br, *intentFile, name, sha, loaded.MergeBase, l.Lane)
}

func runPlannedReview(env *Env, repo *gitx.Repo, st *state.Store, rv review.Reviewer, br *state.Branch, intentFile, name, sha, mergeBase, laneName string) int {
	blocked := func(msg, next string) int { return env.Fail("review", ExitBlocked, msg, next) }
	intent := ""
	if intentFile != "" {
		data, err := os.ReadFile(intentFile) // #nosec G304 -- --intent is an explicitly requested local file
		if err != nil {
			return env.Fail("review", ExitUsage, err.Error(), "fix the --intent path")
		}
		intent = string(data)
	}
	plan, err := planReview(env, repo, br, mergeBase, sha, intent)
	if err != nil {
		if errors.Is(err, review.ErrTooLarge) {
			return blocked(err.Error(), "split the change into smaller commits or branches")
		}
		return blocked(err.Error(), "resolve the git error")
	}
	return finishReview(env, st, rv, plan, name, sha, mergeBase, laneName)
}

func finishReview(env *Env, st *state.Store, rv review.Reviewer, plan reviewPlan, name, sha, mergeBase, laneName string) int {
	blocked := func(msg, next string) int { return env.Fail("review", ExitBlocked, msg, next) }
	resp, used, attempts, err := callReviewer(env, rv, plan, func() error {
		return st.UpdateBranch(name, func(b *state.Branch) error { b.ReviewAttempts++; return nil })
	})
	if err != nil {
		if used > 0 {
			if uerr := st.UpdateBranch(name, func(b *state.Branch) error { b.ReviewCalls += used; return nil }); uerr != nil {
				err = errors.Join(err, uerr)
			}
		}
		next := "retry gate review"
		if errors.Is(err, review.ErrUnavailable) {
			next = "fix reviewer CLI access (gate doctor), or use --reviewer"
		}
		return blocked(err.Error(), next)
	}
	open, err := applyReview(st, name, sha, resp, used)
	if err != nil {
		return blocked(err.Error(), "resolve the state error")
	}
	status, code := state.StatusPass, ExitOK
	for _, f := range open {
		if f.Blocking() {
			status, code = state.StatusFail, ExitFindings
		}
	}
	rec := &state.ReviewRecord{
		SHA: sha, MergeBase: mergeBase, Branch: name, Reviewer: rv.Name(), Delta: plan.delta, Status: status, Findings: open,
		Resolved: resp.Resolved, Finished: time.Now().UTC(), CallsUsed: used, Attempts: attempts,
	}
	if err := st.SaveReview(rec); err != nil {
		return blocked(err.Error(), "resolve the state error")
	}
	res := Result{
		Command: "review", SHA: sha, Lane: laneName, Status: status, Findings: open,
		Message: fmt.Sprintf("reviewer %s: %d new finding(s), %d resolved", rv.Name(), len(resp.Findings), len(resp.Resolved)),
	}
	if code != ExitOK {
		res.NextAction = "fix the blocking review findings, commit, and run gate check"
	} else {
		res.NextAction = "run gate verify if a surface changed, then gate ship"
	}
	return env.Emit(res, code)
}

// reviewGate checks fresh, decision-aware check admission before reuse or a new review.
func reviewGate(st *state.Store, name, sha, mergeBase, laneName string) (br *state.Branch, msg, next string) {
	chk, err := st.LoadCheck(sha)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, err.Error(), "delete the corrupt record under " + st.Dir + " and run gate check"
	}
	if chk == nil || chk.MergeBase != mergeBase || chk.Lane != laneName {
		return nil, "no fresh check record for " + sha[:12], "gate check"
	}
	br, err = st.LoadBranch(name)
	if err != nil {
		return nil, err.Error(), "delete the corrupt record under " + st.Dir
	}
	if !eligible.AssessCheck(chk, br.Decisions, sha).Passing {
		return nil, "check has uncovered findings or failed execution", "gate check or gate decide accept/waive the check finding"
	}
	return br, "", ""
}

// reviewBudget is checked only after an exact-SHA/base/branch review is ruled out.
func reviewBudget(st *state.Store, name string, br *state.Branch) (msg, next string) {
	if br.Parked {
		return "branch is parked", "gate decide accept the open findings, then gate decide approve"
	}
	if br.ReviewCalls >= config.ReviewCalls {
		if err := st.UpdateBranch(name, func(b *state.Branch) error { b.Parked = true; return nil }); err != nil {
			return err.Error(), "resolve the state error"
		}
		return fmt.Sprintf("review budget exhausted (%d/%d calls); branch parked", br.ReviewCalls, config.ReviewCalls), "gate decide"
	}
	return "", ""
}

// reuseReview emits immutable recorded outcome with current branch dispositions;
// it never runs the reviewer, consumes budget or rewrites a failed record to pass.
func reuseReview(env *Env, rec *state.ReviewRecord, br *state.Branch, laneName string) int {
	var findings []state.Finding
	blocking := false
	for _, f := range eligible.Findings(nil, rec, nil, br) {
		if f.Source != state.SourceReview || f.State != state.StateOpen {
			continue
		}
		findings = append(findings, f)
		if f.Blocking() {
			blocking = true
		}
	}
	status, code := state.StatusPass, ExitOK
	if blocking {
		status, code = state.StatusFail, ExitFindings
	}
	message := "reusing review recorded for this exact SHA, comparison base and branch"
	if rec.Status == state.StatusFail && !blocking {
		message += "; stored raw review failed, but decisions cover its blocking claims (see gate status for effective eligibility)"
	}
	res := Result{Command: "review", SHA: rec.SHA, Lane: laneName, Status: status, Findings: findings, Message: message}
	if code == ExitFindings {
		res.NextAction = "run gate status to inspect effective findings and decisions"
	} else {
		res.NextAction = "run gate verify if a surface changed, then gate ship"
	}
	return env.Emit(res, code)
}

// applyReview records a validated response on the branch under the lock and
// returns the review findings that remain open.
func applyReview(st *state.Store, name, sha string, resp *review.Response, used int) (open []state.Finding, err error) {
	err = st.UpdateBranch(name, func(b *state.Branch) error {
		b.ReviewCalls += used
		for _, f := range resp.Findings {
			f.ID = b.NextFindingID()
			b.Findings = append(b.Findings, f)
		}
		for i := range b.Findings {
			if slices.Contains(resp.Resolved, b.Findings[i].ID) && b.Findings[i].State == state.StateOpen {
				b.Findings[i].State = state.StateResolved
			}
		}
		b.LastReviewedSHA = sha
		for _, f := range b.Findings {
			if f.Source == state.SourceReview && f.State == state.StateOpen {
				open = append(open, f)
			}
		}
		return nil
	})
	return open, err
}

func planReview(env *Env, repo *gitx.Repo, br *state.Branch, mergeBase, sha, intent string) (reviewPlan, error) {
	p := reviewPlan{}
	from := mergeBase
	var open []review.OpenFinding
	for _, f := range br.Findings {
		if f.Source == state.SourceReview && f.State == state.StateOpen {
			open = append(open, review.OpenFinding{ID: f.ID, Kind: f.Rule, Path: f.Path, Line: f.Line, Claim: f.Message})
			p.openIDs = append(p.openIDs, f.ID)
		}
	}
	if br.LastReviewedSHA != "" {
		if ok, err := repo.IsAncestor(env.Ctx, br.LastReviewedSHA, sha); err == nil && ok {
			from, p.delta = br.LastReviewedSHA, true
		}
	}
	if !p.delta {
		open, p.openIDs = nil, nil
	}
	changes, err := repo.Changes(env.Ctx, from, sha)
	if err != nil {
		return p, err
	}
	for _, c := range changes {
		p.diffPaths = append(p.diffPaths, c.Path)
	}
	diff, err := repo.DiffRange(env.Ctx, from, sha)
	if err != nil {
		return p, err
	}
	msgs, err := repo.Messages(env.Ctx, mergeBase, sha)
	if err != nil {
		return p, err
	}
	var prior []review.PriorFinding
	if p.delta {
		for _, f := range br.Findings {
			if f.Source == state.SourceReview && f.State != state.StateOpen {
				prior = append(prior, review.PriorFinding{ID: f.ID, State: f.State, Claim: f.Message, Reason: reviewReason(br, f.ID)})
			}
		}
	}
	p.packet, err = review.Build(review.Input{Diff: diff, Messages: msgs, Intent: intent, Open: open, Prior: prior})
	return p, err
}

func reviewReason(b *state.Branch, id string) string {
	for i := len(b.Decisions) - 1; i >= 0; i-- {
		d := b.Decisions[i]
		if d.Kind == state.DecideAccept && d.Target == id {
			return d.Reason
		}
	}
	return "resolved by subsequent review"
}

// callReviewer counts subprocess attempts separately from the one logical review.
// Persist each attempt before execution; a crash cannot erase invocation history.
func callReviewer(env *Env, rv review.Reviewer, p reviewPlan, beforeAttempt func() error) (resp *review.Response, used, attempts int, err error) {
	for range 2 {
		if err := beforeAttempt(); err != nil {
			return nil, used, attempts, err
		}
		attempts++
		raw, _, rerr := rv.Run(env.Ctx, p.packet)
		if errors.Is(rerr, review.ErrUnavailable) && len(raw) == 0 {
			return nil, used, attempts, rerr
		}
		if len(raw) > 0 || errors.Is(rerr, review.ErrInvalidOutput) {
			used = 1
		}
		if rerr != nil && !errors.Is(rerr, review.ErrInvalidOutput) {
			return nil, used, attempts, rerr
		}
		if rerr == nil {
			resp, rerr = review.Parse(raw, p.openIDs, p.diffPaths)
		}
		if rerr == nil {
			return resp, used, attempts, nil
		}
		err = rerr
	}
	return nil, used, attempts, fmt.Errorf("reviewer output invalid after retry: %w", err)
}
