// Package check implements `verify check` (SPEC §4).
package check

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/guard"
	"github.com/mich-murphy/verify/internal/lane"
	"github.com/mich-murphy/verify/internal/pkgmgr"
	"github.com/mich-murphy/verify/internal/runner"
	"github.com/mich-murphy/verify/internal/state"
)

const prekTimeout = 30 * time.Minute

// fastTierBudget is the SPEC §4.5 pre-commit budget; a variable so tests can lower it.
var fastTierBudget = 5 * time.Second

// Options selects what to check.
type Options struct {
	Ref  string // commit-ish; default HEAD
	Fast bool   // guards + pre-commit in place, no record
}

// Outcome is the result of a check. Record is nil only on error.
type Outcome struct {
	Record *state.CheckRecord
	Notes  []string
}

// ToLane converts gitx changes to lane input.
func ToLane(cs []gitx.Change) []lane.Change {
	out := make([]lane.Change, len(cs))
	for i, c := range cs {
		out[i] = lane.Change{Path: c.Path, Added: c.Added, Deleted: c.Deleted, Binary: c.Binary}
	}
	return out
}

type runCtx struct {
	repo      *gitx.Repo
	store     *state.Store
	run       *runner.Runner
	rec       *state.CheckRecord
	unsettled bool
}

// Run checks the exact commit named by opt.Ref.
func Run(ctx context.Context, repo *gitx.Repo, opt Options) (*Outcome, error) {
	ref := opt.Ref
	if ref == "" {
		ref = "HEAD"
	}
	sha, err := repo.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := validateFastRef(ctx, repo, sha, opt.Fast); err != nil {
		return nil, err
	}
	loaded, err := config.Load(ctx, repo, sha)
	if err != nil {
		return nil, err
	}
	cfg := loaded.Config
	changes, err := repo.Changes(ctx, loaded.MergeBase, sha)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(changes))
	for i, c := range changes {
		paths[i] = c.Path
	}
	scope, scopeReason := computeScope(loaded.MergeBase, paths)
	l := lane.Compute(ToLane(changes), cfg, loaded.FromCandidate)
	rec := &state.CheckRecord{
		SHA: sha, MergeBase: loaded.MergeBase, Lane: l.Lane, LaneReasons: l.Reasons,
		Scope: scope, ScopeReason: scopeReason,
		ConfigFromCan: loaded.FromCandidate, Started: time.Now().UTC(),
		Steps: []state.Step{}, Findings: []state.Finding{},
	}
	out := &Outcome{Record: rec, Notes: workingTreeNotes(ctx, repo, sha, opt.Fast)}
	rn, err := runner.New(runner.Config{Env: runner.RepositoryEnv()})
	if err != nil {
		return nil, err
	}
	rc := &runCtx{repo: repo, store: state.Open(repo.CommonDir), run: rn, rec: rec}

	guardBase := loaded.MergeBase
	if opt.Fast {
		guardBase = sha
	}
	gf, err := guard.Run(ctx, guard.Input{Repo: repo, Base: guardBase, SHA: sha, Config: cfg, Fast: opt.Fast})
	if err != nil {
		return nil, fmt.Errorf("guards: %w", err)
	}
	rc.addFindings(gf)

	if opt.Fast {
		if err := rc.runFast(ctx, out); err != nil {
			return nil, err
		}
		return out, nil
	}

	if err := rc.inWorktree(ctx, sha, cfg); err != nil {
		return nil, err
	}
	rec.Finished = time.Now().UTC()
	rec.Status = status(rec)
	if err := rc.store.SaveCheck(rec); err != nil {
		return nil, err
	}
	return out, nil
}

func validateFastRef(ctx context.Context, repo *gitx.Repo, sha string, fast bool) error {
	if !fast {
		return nil
	}
	head, err := repo.Head(ctx)
	if err != nil {
		return err
	}
	if head != sha {
		return fmt.Errorf("fast check requires HEAD; requested %s", sha)
	}
	return nil
}

func (rc *runCtx) runFast(ctx context.Context, out *Outcome) error {
	prek, err := config.PrekConfig(rc.repo.Root)
	if err != nil {
		return err
	}
	if prek != "" {
		if err = rc.fastPrek(ctx, out); err != nil {
			return err
		}
	}
	rc.rec.Finished = time.Now().UTC()
	rc.rec.Status = status(rc.rec)
	return nil
}

func (rc *runCtx) inWorktree(ctx context.Context, sha string, cfg config.Config) (runErr error) {
	dir := rc.store.WorktreesDir()
	unlock, err := state.LockFile(filepath.Join(dir, "check.lock"))
	if err != nil {
		return fmt.Errorf("lock worktree: %w", err)
	}
	defer unlock()
	wt := filepath.Join(dir, "check")
	if err = gitx.CheckWorktreeQuarantine(wt); err != nil {
		return err
	}
	// Claim ownership before reset, audit or repository-configured commands.
	// A crash leaves the marker, so a later invocation cannot reuse the worktree.
	release, err := gitx.BeginWorktreeUse(wt)
	if err != nil {
		return fmt.Errorf("mark worktree in use: %w", err)
	}
	defer func() {
		if !rc.unsettled {
			runErr = errors.Join(runErr, release())
		}
	}()
	if err = rc.repo.ResetWorktree(ctx, wt, sha, []string{"node_modules", ".turbo"}); err != nil {
		// Git's subprocess may still own the path after a failed/cancelled reset.
		rc.unsettled = true
		return fmt.Errorf("prepare worktree: %w", err)
	}
	rep, aerr := audit.Run(ctx, wt, cfg)
	if aerr != nil {
		rc.unsettled = errors.Is(aerr, audit.ErrUnsettled)
		return fmt.Errorf("setup audit: %w", aerr)
	}
	for i := range rep.Obligations {
		rep.Obligations[i].Waived = rep.Obligations[i].Waived || cfg.Waived(rep.Obligations[i].ID)
	}
	rc.addFindings(rep.Findings())
	return rc.runWorktreeSteps(ctx, wt, sha, cfg)
}

func (rc *runCtx) runWorktreeSteps(ctx context.Context, wt, sha string, cfg config.Config) error {
	// prepare: makes the fresh worktree runnable (e.g. npm ci). A failure
	// skips the remaining steps, which could only fail for the same reason.
	prepare := cfg.Prepare
	if len(prepare) == 0 {
		if cmd := pkgmgr.InstallCommand(wt); cmd != nil {
			prepare = []config.Check{{Name: "deps", Run: cmd, Timeout: config.Duration(10 * time.Minute)}}
		}
	}
	for _, p := range prepare {
		passed, err := rc.step(ctx, "prepare-"+p.Name, p.Run, wt, p.Timeout.Std(), true)
		if err != nil {
			return err
		}
		if err = rc.repo.AssertWorktreeIdentity(ctx, wt, sha); err != nil {
			return err
		}
		if !passed {
			return nil
		}
	}
	if prek, err := config.PrekConfig(wt); err != nil {
		return err
	} else if prek != "" {
		for _, st := range config.Stages() {
			if _, err := rc.step(ctx, "prek-"+st, prekArgs(st, rc.rec.Scope, rc.rec.MergeBase, rc.rec.SHA), wt, prekTimeout, true); err != nil {
				return err
			}
			if err := rc.repo.AssertWorktreeIdentity(ctx, wt, sha); err != nil {
				return err
			}
		}
	}
	return nil
}

// fastPrek runs the pre-commit stage in place on the files changed in the
// working tree (SPEC §4.5), and notes when it exceeds the fast-tier budget.
func (rc *runCtx) fastPrek(ctx context.Context, out *Outcome) error {
	files, err := rc.repo.WorktreeChanges(ctx)
	if err != nil {
		return fmt.Errorf("working tree changes: %w", err)
	}
	const name = "prek-pre-commit"
	if len(files) == 0 {
		rc.rec.Steps = append(rc.rec.Steps, state.Step{Name: name, Status: state.StatusSkipped})
		out.Notes = append(out.Notes, "no changed files in the working tree; pre-commit skipped")
		return nil
	}
	argv := append(prekArgs("pre-commit", "", "", ""), "--files")
	argv = append(argv, files...)
	if _, err := rc.step(ctx, name, argv, rc.repo.Root, prekTimeout, false); err != nil {
		return err
	}
	if d := time.Duration(rc.rec.Steps[len(rc.rec.Steps)-1].DurationMS) * time.Millisecond; d > fastTierBudget {
		rc.addFindings([]state.Finding{{
			ID: "check:fast-tier-slow", Source: state.SourceCheck, Rule: "fast-tier-slow",
			Severity: state.SeverityNote,
			Message: fmt.Sprintf("pre-commit stage took %.1fs, over the %.0fs fast-tier budget (SPEC §4.5): move slow hooks to pre-push and scope hooks by types/files",
				d.Seconds(), fastTierBudget.Seconds()),
		}})
	}
	return nil
}

// prekArgs builds the prek command. An affected scope limits the run to the
// merge-base..sha range; full runs all files; an empty scope adds neither (--fast).
func prekArgs(stage, scope, from, to string) []string {
	a := []string{"prek", "run"}
	switch scope {
	case ScopeAffected:
		a = append(a, "--from-ref", from, "--to-ref", to)
	case ScopeFull:
		a = append(a, "--all-files")
	}
	return append(a, "--stage", stage, "--no-progress", "--color", "never")
}

func (rc *runCtx) addFindings(fs []state.Finding) {
	for _, f := range fs {
		if f.State == "" {
			f.State = state.StateOpen
		}
		if f.ID == "" {
			f.ID = f.Source + ":" + f.Rule
		}
		rc.rec.Findings = append(rc.rec.Findings, f)
	}
}

// step runs one command through the runner, logs it and records a finding on failure.
func (rc *runCtx) step(ctx context.Context, name string, argv []string, dir string, timeout time.Duration, persist bool) (passed bool, runErr error) {
	r, err := rc.run.Run(ctx, runner.Command{Args: argv, Dir: dir, Timeout: timeout})
	if persist && err == nil && !r.Settled {
		rc.unsettled = true
	}
	step, log, msg := stepEvidence(name, r, err)
	if persist {
		p, perr := rc.store.LogPath(rc.rec.SHA, name)
		if perr != nil {
			return false, perr
		}
		if perr = state.WriteAtomic(p, []byte(log)); perr != nil {
			return false, perr
		}
		step.Log = p
	}
	rc.rec.Steps = append(rc.rec.Steps, step)
	if step.Status != state.StatusPass {
		text := fmt.Sprintf("step %q failed: %s", name, msg)
		if step.Log != "" {
			text += "; log: " + step.Log
		}
		rc.addFindings([]state.Finding{{
			ID: "check:" + name, Source: state.SourceCheck, Rule: "check:" + name,
			Severity: state.SeverityBlock, Message: text,
		}})
	}
	return step.Status == state.StatusPass, nil
}

func stepEvidence(name string, r runner.Result, err error) (step state.Step, log, msg string) {
	step = state.Step{Name: name, ExitCode: -1, Status: state.StatusFail}
	var b strings.Builder
	if err != nil {
		msg = err.Error()
	} else {
		step.ExitCode = r.ExitCode
		if !r.Started.IsZero() {
			step.DurationMS = r.Finished.Sub(r.Started).Milliseconds()
		}
		if r.Status == runner.Pass {
			step.Status = state.StatusPass
		} else {
			msg = fmt.Sprintf("%s (%s)", r.Status, r.Error)
		}
		b.WriteString(r.Stdout.Output)
		if r.Stderr.Output != "" {
			b.WriteString("\n--- stderr ---\n" + r.Stderr.Output)
		}
	}
	if msg != "" {
		_, _ = fmt.Fprintf(&b, "\n--- verify: %s ---\n", msg)
	}
	return step, b.String(), msg
}

func status(rec *state.CheckRecord) string {
	for _, f := range rec.Findings {
		if f.Blocking() {
			return "fail"
		}
	}
	return "pass"
}

// workingTreeNotes warns when the full check ignores uncommitted work. The fast
// tier checks the working tree itself, so it gets no such note.
func workingTreeNotes(ctx context.Context, repo *gitx.Repo, sha string, fast bool) []string {
	var notes []string
	if head, err := repo.Head(ctx); err == nil && head != sha {
		notes = append(notes, "HEAD differs from the checked commit")
	}
	if fast {
		return notes
	}
	if clean, err := repo.Clean(ctx); err == nil && !clean {
		notes = append(notes, "working tree differs from the checked commit; uncommitted changes are not checked")
	}
	return notes
}
