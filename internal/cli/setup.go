package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/onboard"
	"github.com/mich-murphy/verify/internal/state"
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
	var extra []state.Finding
	if *apply {
		res, aerr := onboard.Apply(env.Ctx, root, rep)
		if aerr != nil {
			return env.Fail("setup", ExitBlocked, "setup --apply: "+aerr.Error(), "fix the error and rerun verify setup --apply")
		}
		if applied = appliedLines(res); len(applied) == 0 && rep.Profile != "unsupported" {
			applied = []string{"nothing to write"}
		}
		extra = res.Findings
		if cfg, err = loadWorkingConfig(root); err == nil {
			rep, err = audit.Run(env.Ctx, root, cfg)
		}
		if err != nil {
			return env.Fail("setup", ExitBlocked, "setup audit after apply: "+err.Error(), "fix the file and rerun verify setup")
		}
	}
	res, perr := setupResult(root, rep, applied, extra, *apply)
	if perr != nil {
		return env.Fail("setup", ExitBlocked, "setup plan: "+perr.Error(), "fix the prek config and rerun verify setup")
	}
	code := ExitOK
	if rep.Blocking() {
		res.Status, code = "fail", ExitFindings
		res.NextAction = "apply the fix snippets above or add a waivers: entry with a reason"
	}
	return env.Emit(res, code)
}

// setupResult renders the audit with the preview of the config setup --apply
// would write (before applying) and the hook-manager notes.
func setupResult(root string, rep audit.Report, applied []string, refused []state.Finding, applying bool) (Result, error) {
	plan, err := onboard.Build(root, rep)
	if err != nil {
		return Result{}, err
	}
	msg := strings.Join(append(applied, "profile: "+rep.Profile, renderAudit(rep)), "\n")
	if !applying && plan.Text != "" {
		msg += "\nwould write " + filepath.Base(plan.Path) + ":\n" + plan.Text
	}
	findings := append(mergeFindings(rep.Findings(), refused), onboard.HookManagers(root, plan.Final())...)
	return Result{Command: "setup", Status: "pass", Findings: findings, Message: msg}, nil
}

// mergeFindings replaces the audit finding of a refused obligation with the
// onboarder's more specific one.
func mergeFindings(audited, refused []state.Finding) []state.Finding {
	out := slices.DeleteFunc(slices.Clone(audited), func(f state.Finding) bool {
		return slices.ContainsFunc(refused, func(r state.Finding) bool { return r.ID == f.ID })
	})
	return append(out, refused...)
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
		if o.Implied {
			continue
		}
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
