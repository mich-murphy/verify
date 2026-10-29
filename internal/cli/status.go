package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mich-murphy/verify/internal/check"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/eligible"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/lane"
	"github.com/mich-murphy/verify/internal/state"
)

func init() { register("status", runStatus) }

// viewErr is a failure to build a view; it maps straight onto Env.Fail.
type viewErr struct {
	code      int
	msg, next string
}

// view is everything known about HEAD, shared by status, decide and ship.
type view struct {
	repo    *gitx.Repo
	store   *state.Store
	sha     string
	branch  string
	loaded  config.Loaded
	changes []gitx.Change
	lane    lane.Result
	recs    records
	elig    eligible.Result
}

func gather(env *Env) (*view, *viewErr) {
	repo, err := gitx.Open(env.Ctx, env.Dir)
	if err != nil {
		return nil, &viewErr{ExitBlocked, "not a git repository: " + err.Error(), "run verify inside a git work tree"}
	}
	sha, err := repo.Head(env.Ctx)
	if err != nil {
		return nil, &viewErr{ExitBlocked, err.Error(), "make a commit first"}
	}
	loaded, err := config.Load(env.Ctx, repo, sha)
	if err != nil {
		if errors.Is(err, config.ErrInvalid) {
			return nil, &viewErr{ExitUsage, err.Error(), "fix verify.yaml"}
		}
		return nil, &viewErr{ExitBlocked, err.Error(), "make sure the base branch exists"}
	}
	changes, err := repo.Changes(env.Ctx, loaded.MergeBase, sha)
	if err != nil {
		return nil, &viewErr{ExitBlocked, err.Error(), "resolve the git error"}
	}
	st := state.Open(repo.CommonDir)
	name, _ := repo.Branch(env.Ctx)
	recs, err := loadRecords(name, repo, st, sha, env)
	if err != nil {
		return nil, &viewErr{ExitBlocked, err.Error(), "delete the corrupt record under " + st.Dir + " and rerun the step"}
	}
	computed := lane.Compute(check.ToLane(changes), loaded.Config, loaded.FromCandidate)
	if recs.chk != nil && (recs.chk.MergeBase != loaded.MergeBase || recs.chk.Lane != computed.Lane) {
		recs.chk = nil
	}
	if recs.rev != nil && (recs.rev.Branch != name || recs.rev.MergeBase != loaded.MergeBase) {
		recs.rev = nil
	}
	if recs.ver != nil && recs.ver.MergeBase != loaded.MergeBase {
		recs.ver = nil
	}
	v := &view{
		repo: repo, store: st, sha: sha, branch: name, loaded: loaded, changes: changes, recs: recs,
		lane: computed,
	}
	paths := make([]string, len(changes))
	for i, c := range changes {
		paths[i] = c.Path
	}
	v.elig = eligible.Evaluate(eligible.Input{
		SHA: sha, MergeBase: loaded.MergeBase, Lane: v.lane.Lane, Check: recs.chk, Review: recs.rev, Verify: recs.ver, Branch: recs.br,
		ChangedPaths: paths, Config: loaded.Config,
	})
	return v, nil
}

func runStatus(env *Env) int {
	fs := env.Flags("status")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("status", ExitUsage, "usage: verify status [--json]", "fix the arguments")
	}
	v, verr := gather(env)
	if verr != nil {
		return env.Fail("status", verr.code, verr.msg, verr.next)
	}
	res := Result{Command: "status", SHA: v.sha, Lane: v.lane.Lane, Status: "ok", Steps: v.recs.stepsOrNil(), Findings: v.recs.findings()}
	if len(v.elig.Blocking) > 0 {
		res.Status = "blocked-by-findings"
	}
	lines := v.recs.summary(config.ReviewCalls)
	lines = append(lines, "SHA: "+v.sha)
	if v.recs.chk != nil {
		lines = append(lines, "check scope: "+v.recs.chk.Scope+" "+v.recs.chk.ScopeReason)
		for _, s := range v.recs.chk.Steps {
			lines = append(lines, fmt.Sprintf("executed check: %s %s (exit %d)", s.Name, s.Status, s.ExitCode))
		}
	}
	if v.recs.ver != nil {
		for _, s := range v.recs.ver.Scenarios {
			lines = append(lines, fmt.Sprintf("scenario: %s %s (exit %d)", s.Name, s.Status, s.ExitCode))
		}
	}
	lines = append(lines, "review findings are claims, not executed reproductions; local results are not CI or merge approval")
	lines = append(lines, eligibilityLines(v.elig)...)
	lines = append(lines, decisionLines(v.recs.br, v.sha)...)
	for _, r := range v.lane.Reasons {
		lines = append(lines, "lane reason: "+r)
	}
	if n := openNotes(res.Findings); n > 0 {
		lines = append(lines, fmt.Sprintf("open notes: %d", n))
	}
	res.NextAction = v.elig.NextAction
	res.Message = strings.Join(lines, "\n")
	return env.Emit(res, ExitOK)
}

func eligibilityLines(e eligible.Result) []string {
	if e.Eligible {
		return []string{"eligible: yes"}
	}
	lines := []string{"eligible: no"}
	for _, m := range e.Missing {
		lines = append(lines, "missing: "+m)
	}
	for _, f := range e.Blocking {
		lines = append(lines, "blocking: "+f.ID)
	}
	return lines
}

func decisionLines(br *state.Branch, sha string) []string {
	var lines []string
	for _, d := range br.Decisions {
		if d.SHA == sha {
			lines = append(lines, strings.TrimSpace(fmt.Sprintf("decision: %s %s (%s)", d.Kind, d.Target, d.Reason)))
		}
	}
	return lines
}

func openNotes(fs []state.Finding) int {
	n := 0
	for _, f := range fs {
		if f.Severity == state.SeverityNote && f.State == state.StateOpen {
			n++
		}
	}
	return n
}

type records struct {
	chk *state.CheckRecord
	rev *state.ReviewRecord
	ver *state.VerifyRecord
	br  *state.Branch
}

func loadRecords(name string, _ *gitx.Repo, st *state.Store, sha string, _ *Env) (records, error) {
	var r records
	var err error
	if r.chk, err = loadOpt(st.LoadCheck(sha)); err != nil {
		return r, err
	}
	if r.rev, err = loadOpt(st.LoadReview(sha)); err != nil {
		return r, err
	}
	if r.ver, err = loadOpt(st.LoadVerify(sha)); err != nil {
		return r, err
	}
	r.br, err = st.LoadBranch(name)
	return r, err
}

func (r records) stepsOrNil() []state.Step {
	if r.chk == nil {
		return nil
	}
	return r.chk.Steps
}

func (r records) findings() []state.Finding {
	return eligible.Findings(r.chk, r.rev, r.ver, r.br)
}

func (r records) summary(budget int) []string {
	lines := []string{
		"check:  " + recStatus(r.chk, func(x *state.CheckRecord) string { return x.Status }),
		"review: " + recStatus(r.rev, func(x *state.ReviewRecord) string { return x.Status }),
		"verify: " + recStatus(r.ver, func(x *state.VerifyRecord) string { return x.Verdict }),
		fmt.Sprintf("review budget: %d/%d logical reviews used", r.br.ReviewCalls, budget),
		fmt.Sprintf("review subprocess attempts: %d total", r.br.ReviewAttempts),
	}
	if r.rev != nil {
		lines = append(lines, fmt.Sprintf("review attempts for SHA: %d", r.rev.Attempts))
	}
	if r.br.Parked {
		lines = append(lines, "branch is PARKED")
	}
	return lines
}

func loadOpt[T any](v *T, err error) (*T, error) {
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	return v, err
}

func recStatus[T any](r *T, f func(*T) string) string {
	if r == nil {
		return "none"
	}
	return f(r)
}
