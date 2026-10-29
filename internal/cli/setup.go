package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/onboard"
)

func init() { register("setup", runSetup) }

func runSetup(env *Env) int {
	fs := env.Flags("setup")
	apply := fs.Bool("apply", false, "write missing prek hooks and install the Git hooks")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("setup", ExitUsage, "usage: verify setup [--apply] [--json]", "fix the arguments")
	}
	root := env.Dir
	if repo, err := gitx.Open(env.Ctx, env.Dir); err == nil {
		root = repo.Root
	}
	cfg, err := loadWorkingConfig(root)
	if err != nil {
		code := ExitBlocked
		if errors.Is(err, config.ErrInvalid) {
			code = ExitUsage
		}
		return env.Fail("setup", code, err.Error(), "fix verify.yaml")
	}
	rep, err := audit.Run(env.Ctx, root, cfg)
	if err != nil {
		return env.Fail("setup", ExitBlocked, "setup audit: "+err.Error(), "fix the unreadable file and rerun verify setup")
	}
	var applied []string
	if *apply {
		res, aerr := onboard.Apply(env.Ctx, root, rep)
		if aerr != nil {
			return env.Fail("setup", ExitBlocked, "setup --apply: "+aerr.Error(), "fix the error and rerun verify setup --apply")
		}
		if applied = appliedLines(res); len(applied) == 0 && rep.Profile != "unsupported" {
			applied = []string{"nothing to write"}
		}
		if cfg, err = loadWorkingConfig(root); err == nil {
			rep, err = audit.Run(env.Ctx, root, cfg)
		}
		if err != nil {
			return env.Fail("setup", ExitBlocked, "setup audit after apply: "+err.Error(), "fix the file and rerun verify setup")
		}
	}
	msg := strings.Join(append(applied, "profile: "+rep.Profile, renderAudit(rep)), "\n")
	res := Result{Command: "setup", Status: "pass", Findings: rep.Findings(), Message: msg}
	code := ExitOK
	if rep.Blocking() {
		res.Status, code = "fail", ExitFindings
		res.NextAction = "apply the fix snippets above or add a waivers: entry with a reason"
	}
	return env.Emit(res, code)
}

func appliedLines(r onboard.Result) []string {
	var out []string
	for _, w := range r.Written {
		out = append(out, "wrote: "+w)
	}
	for _, n := range r.Notes {
		out = append(out, "note: "+n)
	}
	return out
}

func renderAudit(rep audit.Report) string {
	var lines []string
	for _, o := range rep.Obligations {
		line := fmt.Sprintf("  %s: %s", o.ID, o.Status())
		if o.Hook != "" && o.Satisfied {
			line += " (hook " + o.Hook + ")"
		}
		if o.Status() != audit.StatusOK && o.Status() != audit.StatusWaived {
			line += ": " + o.Detail + "\n    fix: " + o.Fix
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func loadWorkingConfig(root string) (config.Config, error) {
	data, err := os.ReadFile(filepath.Join(root, config.FileName)) // #nosec G304 -- fixed name under the repo root
	if errors.Is(err, fs.ErrNotExist) {
		return config.Default(), nil
	}
	if err != nil {
		return config.Config{}, err
	}
	return config.Parse(data)
}
