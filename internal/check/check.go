// Package check implements `verify check` (SPEC §4).
package check

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/eligible"
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

// fullTierBudget is the SPEC §4.5 pre-push budget; a variable so tests can lower it.
var fullTierBudget = 20 * time.Second

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
		out[i] = lane.Change{Path: c.Path, Added: c.Added, Deleted: c.Deleted, Binary: c.Binary, OldMode: c.OldMode, NewMode: c.NewMode}
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

// stepRun is one finished command: its step record, stdout (what prek's hooks print) and full log text.
type stepRun struct {
	step state.Step
	out  string
	log  string
	msg  string
}

// plannedStep is a command the check will run; stage is set for prek stages.
type plannedStep struct {
	name    string
	argv    []string
	timeout time.Duration
	stage   string
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

	blocked, err := rc.guardsBlock(ctx, gf)
	if err != nil {
		return nil, err
	}
	if blocked {
		return rc.shortCircuit(cfg, out)
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

// guardsBlock reports whether a guard finding blocks and no accept decision on
// this SHA covers it. A covered finding lets the hooks run, so accepting a guard
// finding never stands in for them.
func (rc *runCtx) guardsBlock(ctx context.Context, gf []state.Finding) (bool, error) {
	name, err := rc.repo.Branch(ctx)
	if err != nil {
		return false, err
	}
	var decisions []state.Decision
	if name != "" {
		br, err := rc.store.LoadBranch(name)
		if err != nil {
			return false, err
		}
		decisions = br.Decisions
	}
	probe := &state.CheckRecord{Status: state.StatusFail, Findings: gf}
	return len(eligible.AssessCheck(probe, decisions, rc.rec.SHA).Blocking) > 0, nil
}

// shortCircuit records every planned step as skipped and returns without
// entering the worktree. The plan comes from the checkout, not the exact SHA.
func (rc *runCtx) shortCircuit(cfg config.Config, out *Outcome) (*Outcome, error) {
	steps, err := plan(rc.repo.Root, cfg)
	if err != nil {
		return nil, err
	}
	for _, p := range steps {
		rc.rec.Steps = append(rc.rec.Steps, state.Step{Name: p.name, Status: state.StatusSkipped})
	}
	out.Notes = append(out.Notes, "guards blocked; hooks skipped")
	rc.rec.Finished = time.Now().UTC()
	rc.rec.Status = status(rc.rec)
	if err := rc.store.SaveCheck(rc.rec); err != nil {
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
	if rc.rec.Attempt, rc.rec.LogDir, err = rc.store.NewAttempt(sha); err != nil {
		return fmt.Errorf("create attempt logs: %w", err)
	}
	// Claim ownership before reset, audit or repository-configured commands.
	// A crash leaves the marker, so a later invocation cannot reuse the worktree.
	release, err := gitx.BeginWorktreeUse(wt)
	if err != nil {
		return fmt.Errorf("mark worktree in use: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, rc.repo.ReleaseWorktreeUse(release, !rc.unsettled))
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
	steps, err := plan(wt, cfg)
	if err != nil {
		return err
	}
	for i, p := range steps {
		passed, err := rc.runPlanned(ctx, p, wt, sha)
		if err != nil {
			return err
		}
		if !passed {
			// Later steps could only fail for the same reason; record them as not run.
			for _, later := range steps[i+1:] {
				rc.rec.Steps = append(rc.rec.Steps, state.Step{Name: later.name, Status: state.StatusSkipped})
			}
			return nil
		}
	}
	return nil
}

// plan lists the steps in run order: prepare (configured, else derived from the
// lockfile, which makes the fresh worktree runnable), then each prek stage.
func plan(wt string, cfg config.Config) ([]plannedStep, error) {
	prepare := cfg.Prepare
	if len(prepare) == 0 {
		if cmd := pkgmgr.InstallCommand(wt); cmd != nil {
			prepare = []config.Check{{Name: "deps", Run: cmd, Timeout: config.Duration(10 * time.Minute)}}
		}
	}
	var steps []plannedStep
	for _, p := range prepare {
		steps = append(steps, plannedStep{name: "prepare-" + p.Name, argv: p.Run, timeout: p.Timeout.Std()})
	}
	prek, err := config.PrekConfig(wt)
	if err != nil || prek == "" {
		return steps, err
	}
	for _, st := range config.Stages() {
		steps = append(steps, plannedStep{name: "prek-" + st, stage: st, timeout: prekTimeout})
	}
	return steps, nil
}

func (rc *runCtx) runPlanned(ctx context.Context, p plannedStep, wt, sha string) (bool, error) {
	argv := p.argv
	if p.stage != "" {
		argv = prekArgs(p.stage, rc.rec.Scope, rc.rec.MergeBase, rc.rec.SHA)
	}
	sr, err := rc.exec(ctx, p.name, argv, wt, p.timeout, true)
	if err != nil {
		return false, err
	}
	if p.stage == "" {
		rc.record(sr, stepFindings(p.name, sr))
	} else if err = rc.finishStage(ctx, p.stage, wt, sr); err != nil {
		return false, err
	}
	if err = rc.repo.AssertWorktreeIdentity(ctx, wt, sha); err != nil {
		return false, err
	}
	return sr.step.Status == state.StatusPass, nil
}

// finishStage records a prek stage. A failed stage that rewrote tracked files is
// restored once its findings name them, so the identity check still guards the rest.
func (rc *runCtx) finishStage(ctx context.Context, stage, wt string, sr stepRun) error {
	hooks := parsePrekOutput([]byte(sr.out))
	sr.step.Hooks = hookResults(hooks)
	var paths []string
	if sr.step.Status != state.StatusPass {
		var err error
		if paths, err = rc.repo.TrackedChanges(ctx, wt); err != nil {
			return err
		}
	}
	rc.record(sr, stageFindings(stage, sr, hooks, paths))
	rc.addFindings(budgetNote(stage, time.Duration(sr.step.DurationMS)*time.Millisecond, stageBudget(stage), sr.step.Hooks))
	if len(paths) > 0 {
		return rc.repo.RestoreTracked(ctx, wt)
	}
	return nil
}

func stageBudget(stage string) time.Duration {
	if stage == "pre-commit" {
		return fastTierBudget
	}
	return fullTierBudget
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
	sr, err := rc.exec(ctx, name, argv, rc.repo.Root, prekTimeout, false)
	if err != nil {
		return err
	}
	hooks := parsePrekOutput([]byte(sr.out))
	sr.step.Hooks = hookResults(hooks)
	rc.record(sr, stageFindings("pre-commit", sr, hooks, nil))
	rc.addFindings(budgetNote("pre-commit", time.Duration(sr.step.DurationMS)*time.Millisecond, fastTierBudget, sr.step.Hooks))
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
	// --verbose adds each hook's id and duration to passing hooks, which the budget note needs.
	return append(a, "--stage", stage, "--no-progress", "--color", "never", "--verbose")
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

// exec runs one command through the runner and, when persist is set, writes its
// log under the attempt's log directory. The caller records the result.
func (rc *runCtx) exec(ctx context.Context, name string, argv []string, dir string, timeout time.Duration, persist bool) (stepRun, error) {
	r, err := rc.run.Run(ctx, runner.Command{Args: argv, Dir: dir, Timeout: timeout})
	if persist && err == nil && !r.Settled {
		rc.unsettled = true
	}
	sr := stepEvidence(name, r, err)
	sr.out = r.Stdout.Output
	if persist {
		sr.step.Log = filepath.Join(rc.rec.LogDir, url.PathEscape(name)+".log")
		if err = state.WriteAtomic(sr.step.Log, []byte(sr.log)); err != nil {
			return stepRun{}, err
		}
	}
	return sr, nil
}

func (rc *runCtx) record(sr stepRun, fs []state.Finding) {
	rc.rec.Steps = append(rc.rec.Steps, sr.step)
	rc.addFindings(fs)
}

// stepFindings is the generic failure finding for a prepare step.
func stepFindings(name string, sr stepRun) []state.Finding {
	if sr.step.Status == state.StatusPass {
		return nil
	}
	return []state.Finding{stepFailure(name, sr)}
}

func stepFailure(name string, sr stepRun) state.Finding {
	text := fmt.Sprintf("step %q failed: %s", name, sr.msg)
	if sr.step.Log != "" {
		text += "; log: " + sr.step.Log
	}
	id := "check:" + name
	return state.Finding{
		ID: id, Source: state.SourceCheck, Rule: id, Severity: state.SeverityBlock,
		Message: text, Log: sr.step.Log, Excerpt: excerpt(strings.Split(strings.TrimSpace(sr.log), "\n"), true),
	}
}

func stepEvidence(name string, r runner.Result, err error) (sr stepRun) {
	sr.step = state.Step{Name: name, ExitCode: -1, Status: state.StatusFail}
	var b strings.Builder
	if err != nil {
		sr.msg = err.Error()
	} else {
		sr.step.ExitCode = r.ExitCode
		if !r.Started.IsZero() {
			sr.step.DurationMS = r.Finished.Sub(r.Started).Milliseconds()
		}
		if r.Status == runner.Pass {
			sr.step.Status = state.StatusPass
		} else {
			sr.msg = fmt.Sprintf("%s (%s)", r.Status, r.Error)
		}
		b.WriteString(r.Stdout.Output)
		if r.Stderr.Output != "" {
			b.WriteString("\n--- stderr ---\n" + r.Stderr.Output)
		}
	}
	if sr.msg != "" {
		_, _ = fmt.Fprintf(&b, "\n--- verify: %s ---\n", sr.msg)
	}
	sr.log = b.String()
	return sr
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
