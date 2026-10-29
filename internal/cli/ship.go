package cli

import (
	"strings"

	"github.com/mich-murphy/verify/internal/ship"
	"github.com/mich-murphy/verify/internal/state"
)

func init() { register("ship", runShip) }

func runShip(env *Env) int {
	fs := env.Flags("ship")
	dry := fs.Bool("dry-run", false, "print the plan and run nothing")
	remote := fs.String("remote", "origin", "git remote to push to")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("ship", ExitUsage, "usage: gate ship [--dry-run] [--remote NAME] [--json]", "fix the arguments")
	}
	v, verr := gather(env)
	if verr != nil {
		return env.Fail("ship", verr.code, verr.msg, verr.next)
	}
	if refusal := v.shipRefusal(env); refusal != nil {
		return env.Emit(Result{
			Command: "ship", SHA: v.sha, Lane: v.lane.Lane, Status: "blocked",
			Message: refusal.msg, NextAction: refusal.next, Findings: v.elig.Blocking,
		}, ExitBlocked)
	}
	title, err := v.repo.FirstSubject(env.Ctx, v.loaded.MergeBase, v.sha)
	if err != nil || title == "" {
		title = v.branch
	}
	rep, err := ship.Run(env.Ctx, ship.Options{
		Repo: v.repo, Remote: *remote, Branch: v.branch, SHA: v.sha, Base: v.loaded.Config.Base,
		Title: title, Lane: v.elig.Lane, DryRun: *dry,
		Body: ship.Body(ship.BodyInput{SHA: v.sha, Lane: v.elig.Lane, LaneReasons: v.lane.Reasons, Check: v.recs.chk, Review: v.recs.rev, Verify: v.recs.ver, Branch: v.recs.br}),
	})
	res := Result{Command: "ship", SHA: v.sha, Lane: v.lane.Lane, Status: "shipped", Steps: shipSteps(rep), Message: shipMessage(rep)}
	switch {
	case err != nil:
		res.Status = "blocked"
		res.Message += "\n" + doneRemaining(rep)
		res.NextAction = "resolve the error above and rerun gate ship; completed steps are safe to repeat"
		return env.Emit(res, ExitBlocked)
	case *dry:
		res.Status = "dry-run"
	}
	return env.Emit(res, ExitOK)
}

func (v *view) shipRefusal(env *Env) *viewErr {
	switch {
	case v.branch == "":
		return &viewErr{msg: "HEAD is detached; ship needs a branch", next: "check out a branch"}
	case !v.elig.Eligible:
		return &viewErr{msg: "not eligible to ship:\n" + strings.Join(v.elig.Missing, "\n"), next: v.elig.NextAction}
	}
	clean, err := v.repo.Clean(env.Ctx)
	if err != nil {
		return &viewErr{msg: err.Error(), next: "resolve the git error"}
	}
	if !clean {
		return &viewErr{msg: "working tree is not clean; only the committed SHA ships", next: "commit or stash the changes, then run gate check"}
	}
	return nil
}

func shipSteps(rep ship.Report) []state.Step {
	var out []state.Step
	for _, s := range rep.Steps {
		st := state.StatusSkipped
		switch s.Status {
		case ship.Done:
			st = state.StatusPass
		case ship.Failed:
			st = state.StatusFail
		}
		out = append(out, state.Step{Name: "ship:" + s.Name, Status: st})
	}
	return out
}

func shipMessage(rep ship.Report) string {
	var l []string
	for _, s := range rep.Steps {
		l = append(l, strings.TrimSpace(s.Name+" "+s.Status+": "+s.Detail))
	}
	return strings.Join(l, "\n")
}

func doneRemaining(rep ship.Report) string {
	var done, left []string
	for _, s := range rep.Steps {
		if s.Status == ship.Done {
			done = append(done, s.Name)
		} else {
			left = append(left, s.Name)
		}
	}
	return "done: " + strings.Join(done, ", ") + "; remaining: " + strings.Join(left, ", ")
}
