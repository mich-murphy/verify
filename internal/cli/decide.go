package cli

import (
	"flag"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/eligible"
	"github.com/mich-murphy/verify/internal/state"
)

func init() { register("decide", runDecide) }

const decideUsage = "usage: verify decide approve|accept ID|waive OBLIGATION --reason TEXT [--json]"

// parseDecide splits "<kind> [target] --reason R" in any flag order.
func parseDecide(env *Env, fs *flag.FlagSet, reason *string) (kind, target string, ok bool) {
	var pos []string
	rest := env.Args
	for {
		if err := fs.Parse(rest); err != nil {
			return "", "", false
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if strings.TrimSpace(*reason) == "" || len(pos) == 0 || len(pos) > 2 {
		return "", "", false
	}
	kind = pos[0]
	if len(pos) == 2 {
		target = pos[1]
	}
	if (kind == state.DecideApprove) != (target == "") {
		return "", "", false
	}
	switch kind {
	case state.DecideApprove, state.DecideAccept, state.DecideWaive:
		return kind, target, true
	}
	return "", "", false
}

func runDecide(env *Env) int {
	fs := env.Flags("decide")
	reason := fs.String("reason", "", "why (required)")
	kind, target, ok := parseDecide(env, fs, reason)
	if !ok {
		return env.Fail("decide", ExitUsage, decideUsage+"; --reason is required", "fix the arguments")
	}
	v, verr := gather(env)
	if verr != nil {
		return env.Fail("decide", verr.code, verr.msg, verr.next)
	}
	if v.branch == "" {
		return env.Fail("decide", ExitBlocked, "HEAD is detached; decisions are recorded per branch", "check out a branch")
	}
	if kind != state.DecideApprove && v.recs.chk == nil {
		return env.Fail("decide", ExitBlocked, "no check record for HEAD", "run verify check")
	}
	target, msg := normaliseTarget(v, kind, target)
	if msg != "" {
		return env.Fail("decide", ExitUsage, msg, "run verify status to see finding IDs")
	}
	err := recordDecision(v, kind, target, *reason)
	if err != nil {
		return env.Fail("decide", ExitBlocked, err.Error(), "resolve the state error and retry")
	}
	return env.Emit(Result{
		Command: "decide", SHA: v.sha, Lane: v.lane.Lane, Status: "ok",
		Message:    strings.TrimSpace("recorded " + kind + " " + target + " on " + v.sha[:12]),
		NextAction: "run verify status",
	}, ExitOK)
}

func recordDecision(v *view, kind, target, reason string) error {
	return v.store.UpdateBranch(v.branch, func(b *state.Branch) error {
		b.Decisions = append(b.Decisions, state.Decision{Kind: kind, Target: target, Reason: strings.TrimSpace(reason), SHA: v.sha, Time: time.Now().UTC()})
		if kind == state.DecideAccept {
			acceptBranchFinding(b, v.recs.rev, target)
		}
		return nil
	})
}

// acceptBranchFinding recovers immutable review evidence when branch history was lost.
func acceptBranchFinding(b *state.Branch, rec *state.ReviewRecord, target string) {
	if rec != nil {
		for _, f := range rec.Findings {
			if f.ID == target && f.Source == state.SourceReview && !branchHasFinding(b, target) {
				b.Findings = append(b.Findings, f)
			}
		}
	}
	for i := range b.Findings {
		if b.Findings[i].ID == target && b.Findings[i].Source == state.SourceReview {
			b.Findings[i].State = state.StateAccepted
		}
	}
}

// normaliseTarget validates the target and returns the canonical form, or a usage message.
func normaliseTarget(v *view, kind, target string) (string, string) {
	switch kind {
	case state.DecideAccept:
		return target, acceptProblem(v, target)
	case state.DecideWaive:
		if target == "verify" {
			if v.recs.ver == nil || v.recs.ver.SHA != v.sha || v.recs.ver.MergeBase != v.loaded.MergeBase || v.recs.ver.Verdict != "inconclusive" {
				return target, "verify waiver requires an inconclusive verify record for this SHA and comparison base"
			}
			return target, ""
		}
		if !strings.HasPrefix(target, "setup:") {
			target = "setup:" + target
		}
		for _, f := range v.recs.chk.Findings {
			if f.ID == target && f.Source == state.SourceSetup {
				return target, ""
			}
		}
		return target, "unknown waive target " + target + ": expected verify or a setup obligation finding on HEAD"
	}
	return target, ""
}

func branchHasFinding(b *state.Branch, id string) bool {
	for _, f := range b.Findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func acceptProblem(v *view, id string) string {
	for _, f := range eligible.Findings(nil, v.recs.rev, nil, v.recs.br) {
		if f.ID == id && f.Source == state.SourceReview && f.State == state.StateOpen {
			return ""
		}
	}
	for _, f := range v.recs.chk.Findings {
		if f.ID != id {
			continue
		}
		switch f.Source {
		case state.SourceGuard:
			return ""
		case state.SourceSetup:
			return id + " is a setup finding: use verify decide waive " + id
		}
		return id + " is a failing check and cannot be accepted; fix it"
	}
	return "unknown finding ID " + id
}
