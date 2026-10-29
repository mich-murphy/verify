// Package verify implements `verify verify` (SPEC §6): run configured CLI
// scenarios in a detached worktree when a declared surface changed.
package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/glob"
	"github.com/mich-murphy/verify/internal/runner"
	"github.com/mich-murphy/verify/internal/state"
)

// Verdicts.
const (
	Go           = "go"
	NoGo         = "no-go"
	Inconclusive = "inconclusive"
	NoSurface    = "no-surface"
)

const setupTimeout = 10 * time.Minute

// Options selects what to verify.
type Options struct {
	Ref string // commit-ish; default HEAD
}

// Run verifies the commit named by opt.Ref and saves the record.
func Run(ctx context.Context, repo *gitx.Repo, opt Options) (*state.VerifyRecord, error) {
	ref := opt.Ref
	if ref == "" {
		ref = "HEAD"
	}
	sha, err := repo.Resolve(ctx, ref)
	if err != nil {
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
	rec := &state.VerifyRecord{
		Schema: state.SchemaVerify, SHA: sha, MergeBase: loaded.MergeBase, Verdict: NoSurface,
		Steps: []state.Step{}, Findings: []state.Finding{},
	}
	store := state.Open(repo.CommonDir)
	surface := false
	for _, c := range changes {
		if glob.MatchAny(cfg.Verify.Surfaces, c.Path) {
			surface = true
			break
		}
	}
	if surface {
		if err := runInWorktree(ctx, repo, store, sha, cfg.Verify, rec); err != nil {
			return nil, err
		}
	}
	rec.Finished = time.Now().UTC()
	if err := store.SaveVerify(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func runInWorktree(ctx context.Context, repo *gitx.Repo, store *state.Store, sha string, v config.Verify, rec *state.VerifyRecord) (runErr error) {
	dir := store.WorktreesDir()
	unlock, err := state.LockFile(filepath.Join(dir, "verify.lock"))
	if err != nil {
		return fmt.Errorf("lock worktree: %w", err)
	}
	defer unlock()
	wt := filepath.Join(dir, "verify")
	if err = gitx.CheckWorktreeQuarantine(wt); err != nil {
		return err
	}
	// Claim ownership before reset, setup or any scenario; a killed Verify
	// process cannot silently release the right to reuse its running worktree.
	release, err := gitx.BeginWorktreeUse(wt)
	if err != nil {
		return fmt.Errorf("mark worktree in use: %w", err)
	}
	settled := true
	defer func() {
		if settled {
			runErr = errors.Join(runErr, release())
		}
	}()
	if err = repo.ResetWorktree(ctx, wt, sha, []string{"node_modules", ".turbo"}); err != nil {
		settled = false // Git may still own the path after a failed reset.
		return fmt.Errorf("prepare worktree: %w", err)
	}
	rn, err := runner.New(runner.Config{Env: runner.RepositoryEnv()})
	if err != nil {
		return err
	}
	w := &work{store: store, repo: repo, sha: sha, run: rn, rec: rec, dir: wt}
	defer func() { settled = settled && !w.unsettled }()
	rec.Verdict = Go
	if len(v.Setup) > 0 {
		res, ok, err := w.exec(ctx, "verify-setup", v.Setup, nil, setupTimeout)
		if err != nil {
			return err
		}
		if !ok || res.Status != runner.Pass {
			w.inconclusive("setup failed: " + describe(res))
			return nil
		}
	}
	inconclusive := false
	for _, sc := range v.Scenarios {
		ok, err := w.scenario(ctx, sc)
		if err != nil {
			return err
		}
		if !ok {
			inconclusive = true
		}
	}
	switch {
	case hasRule(rec, false):
		rec.Verdict = NoGo
	case inconclusive:
		rec.Verdict = Inconclusive
	}
	return nil
}

type work struct {
	store     *state.Store
	repo      *gitx.Repo
	sha       string
	run       *runner.Runner
	rec       *state.VerifyRecord
	dir       string
	unsettled bool
}

func hasRule(rec *state.VerifyRecord, inconclusive bool) bool {
	for _, f := range rec.Findings {
		if (f.Rule == "verify:inconclusive") == inconclusive {
			return true
		}
	}
	return false
}

func (w *work) inconclusive(msg string) {
	w.rec.Verdict = Inconclusive
	w.finding("verify:inconclusive", msg)
}

func (w *work) finding(rule, msg string) {
	id := rule
	for n := 2; findingIDUsed(w.rec.Findings, id); n++ {
		id = fmt.Sprintf("%s:%d", rule, n)
	}
	w.rec.Findings = append(w.rec.Findings, state.Finding{
		ID: id, Source: state.SourceVerify, Rule: rule,
		Severity: state.SeverityBlock, Message: msg, State: state.StateOpen,
	})
}

func findingIDUsed(fs []state.Finding, id string) bool {
	for _, f := range fs {
		if f.ID == id {
			return true
		}
	}
	return false
}

func describe(r runner.Result) string {
	if r.Error != "" {
		return fmt.Sprintf("%s (%s)", r.Status, r.Error)
	}
	return fmt.Sprintf("%s (exit %d)", r.Status, r.ExitCode)
}

// exec runs argv directly, records its log, and verifies tracked identity.
func (w *work) exec(ctx context.Context, name string, argv []string, stdin []byte, timeout time.Duration) (runner.Result, bool, error) {
	r, err := w.run.Run(ctx, runner.Command{Args: argv, Dir: w.dir, Timeout: timeout, Stdin: stdin})
	if err == nil && !r.Settled {
		w.unsettled = true
	}
	step := state.Step{Name: name, ExitCode: -1, Status: state.StatusFail}
	var log strings.Builder
	if err != nil {
		r.Status = runner.Fail
		r.Error = err.Error()
	} else {
		step.ExitCode = r.ExitCode
		if !r.Started.IsZero() {
			step.DurationMS = r.Finished.Sub(r.Started).Milliseconds()
		}
		if r.Status == runner.Pass {
			step.Status = state.StatusPass
		}
		log.WriteString(r.Stdout.Output)
		if r.Stderr.Output != "" {
			log.WriteString("\n--- stderr ---\n" + r.Stderr.Output)
		}
	}
	if r.Error != "" {
		fmt.Fprintf(&log, "\n--- verify: %s (%s) ---\n", r.Status, r.Error)
	}
	p, perr := w.store.LogPath(w.rec.SHA, name)
	if perr != nil {
		return r, false, perr
	}
	if perr = state.WriteAtomic(p, []byte(log.String())); perr != nil {
		return r, false, perr
	}
	step.Log = p
	w.rec.Steps = append(w.rec.Steps, step)
	if perr = w.repo.AssertWorktreeIdentity(ctx, w.dir, w.sha); perr != nil {
		return r, false, perr
	}
	return r, true, nil
}

// scenario runs one scenario; it reports false when the outcome was inconclusive.
func (w *work) scenario(ctx context.Context, sc config.Scenario) (bool, error) {
	timeout := sc.Timeout.Std()
	if timeout <= 0 {
		timeout = time.Minute
	}
	sr := state.ScenarioResult{Name: sc.Name, ExitCode: -1, Status: state.StatusFail}
	defer func() { w.rec.Scenarios = append(w.rec.Scenarios, sr) }()

	argv := append([]string(nil), sc.Run...)
	if strings.ContainsRune(argv[0], os.PathSeparator) && !filepath.IsAbs(argv[0]) {
		argv[0] = filepath.Join(w.dir, argv[0])
		if _, err := os.Stat(argv[0]); err != nil {
			sr.Detail = "binary missing: " + sc.Run[0]
			w.inconclusive(fmt.Sprintf("scenario %q: %s", sc.Name, sr.Detail))
			return false, nil
		}
	}
	res, ok, err := w.exec(ctx, "verify-scenario-"+sc.Name, argv, []byte(sc.Stdin), timeout)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	sr.ExitCode = res.ExitCode
	switch {
	case res.Status == runner.TimedOut || res.Status == runner.Cancelled || !res.Settled || res.Started.IsZero():
		sr.Detail = "did not complete: " + describe(res)
		w.inconclusive(fmt.Sprintf("scenario %q: %s", sc.Name, sr.Detail))
		return false, nil
	case res.Status != runner.Pass && res.ExitCode == 0:
		sr.Detail = "could not run: " + describe(res)
		w.inconclusive(fmt.Sprintf("scenario %q: %s", sc.Name, sr.Detail))
		return false, nil
	}
	bad, undecidable := assertions(sc, res)
	if bad != "" {
		sr.Detail = bad
		w.finding("verify:scenario:"+sc.Name, fmt.Sprintf("scenario %q: %s", sc.Name, bad))
		return true, nil
	}
	if undecidable {
		sr.Detail = "stdout truncated; assertion cannot be decided"
		w.inconclusive(fmt.Sprintf("scenario %q: %s", sc.Name, sr.Detail))
		return false, nil
	}
	sr.Status = state.StatusPass
	return true, nil
}

func assertions(sc config.Scenario, res runner.Result) (string, bool) {
	var bad []string
	unknown := false
	if res.ExitCode != sc.Exit {
		bad = append(bad, fmt.Sprintf("exit %d, want %d", res.ExitCode, sc.Exit))
	}
	out := []byte(res.Stdout.Output)
	if sc.StdoutContains != nil && !bytes.Contains(out, []byte(*sc.StdoutContains)) {
		if res.Stdout.Truncated {
			unknown = true
		} else {
			bad = append(bad, fmt.Sprintf("stdout does not contain %q", *sc.StdoutContains))
		}
	}
	if sc.StdoutEquals != nil {
		want := []byte(*sc.StdoutEquals)
		switch {
		case res.Stdout.Truncated && res.Stdout.Bytes == int64(len(want)) && bytes.HasPrefix(want, out):
			unknown = true // Only the uncaptured suffix remains undecidable.
		case res.Stdout.Truncated || !bytes.Equal(out, want):
			bad = append(bad, fmt.Sprintf("stdout differs from expected %q", *sc.StdoutEquals))
		}
	}
	return strings.Join(bad, "; "), unknown
}
