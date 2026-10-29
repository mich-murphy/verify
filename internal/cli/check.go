package cli

import (
	"errors"
	"strings"

	"github.com/mich-murphy/verify/internal/check"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
)

func init() { register("check", runCheck) }

func runCheck(env *Env) int {
	fs := env.Flags("check")
	sha := fs.String("sha", "", "commit to check (default HEAD)")
	fast := fs.Bool("fast", false, "guards and pre-commit in place; no record")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("check", ExitUsage, "usage: verify check [--sha REF] [--fast] [--json]", "fix the arguments")
	}
	repo, err := gitx.Open(env.Ctx, env.Dir)
	if err != nil {
		return env.Fail("check", ExitBlocked, "not a git repository: "+err.Error(), "run verify inside a git work tree")
	}
	out, err := check.Run(env.Ctx, repo, check.Options{Ref: *sha, Fast: *fast})
	if err != nil {
		if errors.Is(err, config.ErrInvalid) {
			return env.Fail("check", ExitUsage, err.Error(), "fix verify.yaml")
		}
		return env.Fail("check", ExitBlocked, err.Error(), "resolve the error and run verify check again")
	}
	rec := out.Record
	res := Result{
		Command: "check", SHA: rec.SHA, Lane: rec.Lane, Status: rec.Status,
		Steps: rec.Steps, Findings: rec.Findings,
	}
	var msg []string
	if rec.Scope != "" {
		line := "scope: " + rec.Scope
		if rec.ScopeReason != "" {
			line += " (" + rec.ScopeReason + ")"
		}
		msg = append(msg, line)
	}
	for _, r := range rec.LaneReasons {
		msg = append(msg, "note: "+r)
	}
	for _, n := range out.Notes {
		msg = append(msg, "note: "+n)
	}
	if *fast {
		msg = append(msg, "note: --fast wrote no record and is never accepted by ship")
	}
	res.Message = strings.Join(msg, "\n")
	if rec.Status == "fail" {
		res.NextAction = "fix the blocking findings, commit, and run verify check again"
		return env.Emit(res, ExitFindings)
	}
	return env.Emit(res, ExitOK)
}
