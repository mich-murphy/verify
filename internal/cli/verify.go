package cli

import (
	"errors"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/verify"
)

func init() { register("verify", runVerify) }

func runVerify(env *Env) int {
	fs := env.Flags("verify")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("verify", ExitUsage, "usage: gate verify [--json]", "fix the arguments")
	}
	repo, err := gitx.Open(env.Ctx, env.Dir)
	if err != nil {
		return env.Fail("verify", ExitBlocked, "not a git repository: "+err.Error(), "run gate inside a git work tree")
	}
	rec, err := verify.Run(env.Ctx, repo, verify.Options{})
	if err != nil {
		if errors.Is(err, config.ErrInvalid) {
			return env.Fail("verify", ExitUsage, err.Error(), "fix gate.yaml")
		}
		return env.Fail("verify", ExitBlocked, err.Error(), "resolve the error and run gate verify again")
	}
	res := Result{Command: "verify", SHA: rec.SHA, Status: rec.Verdict, Steps: rec.Steps, Findings: rec.Findings}
	if rec.Verdict == verify.Go || rec.Verdict == verify.NoSurface {
		return env.Emit(res, ExitOK)
	}
	res.NextAction = "fix the failing scenarios, commit, and run gate verify again"
	if rec.Verdict == verify.Inconclusive {
		res.NextAction = "fix verify setup or the scenario, or waive with: gate decide waive verify --reason TEXT"
	}
	return env.Emit(res, ExitFindings)
}
