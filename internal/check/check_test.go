package check

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/audit"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/state"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper with fixed executable
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func put(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newRepo builds main + feature with the given verify.yaml on main and one change on feature.
func newRepo(t *testing.T, yaml string, prekConfig bool) *gitx.Repo {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			_ = os.Unsetenv(k)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "verify.yaml", yaml)
	put(t, dir, "a.go", "package a\n")
	if prekConfig {
		put(t, dir, ".pre-commit-config.yaml", "repos: []\n")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "feature")
	put(t, dir, "a.go", "package a\n\nvar X = 1\n")
	git(t, dir, "commit", "-qam", "change")
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func fakePrek(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\necho \"prek $*\" >> \"$PWD/../prek-calls.log\" 2>/dev/null\nif [ -f fail-hook ]; then exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte(script), 0o700); err != nil { // #nosec G306 -- fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const head = ""

func TestPrepareRunsFirstAndFailureSkipsRest(t *testing.T) {
	fakePrek(t)
	ok := "prepare:\n  - {name: deps, run: [sh, -c, \"touch prepared\"]}\n"
	out, err := Run(t.Context(), newRepo(t, ok, true), Options{})
	if err != nil || out.Record.Status != "pass" || len(out.Record.Steps) != 3 || out.Record.Steps[0].Name != "prepare-deps" {
		t.Fatalf("%+v %v", out.Record, err)
	}
	bad := "prepare:\n  - {name: deps, run: [sh, -c, \"exit 1\"]}\n"
	repo := newRepo(t, bad, true)
	out, err = Run(t.Context(), repo, Options{})
	if err != nil || out.Record.Status != "fail" || len(out.Record.Steps) != 3 {
		t.Fatalf("%+v %v", out.Record, err)
	}
	if f := out.Record.Findings; !slices.ContainsFunc(f, func(x state.Finding) bool { return x.ID == "check:prepare-deps" }) {
		t.Fatalf("%+v", f)
	}
	if _, err := os.Stat(filepath.Join(repo.CommonDir, "verify", "worktrees", "check")); err != nil {
		t.Fatalf("worktree not kept for reuse: %v", err)
	}
}

func TestPrepareTimeoutAndMissingTool(t *testing.T) {
	for _, y := range []string{"prepare:\n  - {name: slow, run: [sleep, \"5\"], timeout: 200ms}\n", "prepare:\n  - {name: m, run: [definitely-not-a-tool]}\n"} {
		out, err := Run(t.Context(), newRepo(t, y, false), Options{})
		if err != nil || out.Record.Status != "fail" || len(out.Record.Steps) != 1 {
			t.Fatalf("%+v %v", out.Record, err)
		}
	}
}

// With no prepare: in verify.yaml, the root lockfile in the worktree decides.
func TestDerivedPrepareFromLockfile(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\ntouch installed\n"), 0o700); err != nil { // #nosec G306 -- fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := newRepo(t, head, false)
	put(t, repo.Root, "package-lock.json", "{}\n")
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "lock")
	out, err := Run(t.Context(), repo, Options{})
	if err != nil || out.Record.Status != "fail" || len(out.Record.Steps) != 1 || out.Record.Steps[0].Name != "prepare-deps" {
		t.Fatalf("%+v %v", out.Record, err)
	}
	if !slices.ContainsFunc(out.Record.Findings, func(f state.Finding) bool { return f.ID == "setup:prek-config" && f.Blocking() }) {
		t.Fatal("missing prek config must block")
	}
}

func TestPrekStagesAndFast(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, head, true)
	out, err := Run(t.Context(), repo, Options{})
	if err != nil || out.Record.Status != "pass" || len(out.Record.Steps) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
	if out.Record.Steps[0].Name != "prek-pre-commit" || out.Record.Steps[1].Name != "prek-pre-push" {
		t.Fatalf("%+v", out.Record.Steps)
	}
	// fast: one step, in place, no new record
	fast := newRepo(t, head, true)
	o, err := Run(t.Context(), fast, Options{Fast: true})
	if err != nil || len(o.Record.Steps) != 1 || o.Record.Status != "pass" {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := state.Open(fast.CommonDir).LoadCheck(o.Record.SHA); err == nil {
		t.Fatal("fast mode wrote a record")
	}
	put(t, fast.Root, "fail-hook", "x")
	o, err = Run(t.Context(), fast, Options{Fast: true})
	if err != nil || o.Record.Status != "fail" {
		t.Fatalf("%+v %v", o, err)
	}
	// --fast checks the working tree, so it must not claim uncommitted changes are unchecked.
	for _, n := range o.Notes {
		if strings.Contains(n, "uncommitted changes are not checked") {
			t.Fatalf("misleading note in fast mode: %q", n)
		}
	}
}

func TestBadRefAndConfig(t *testing.T) {
	repo := newRepo(t, head, false)
	if _, err := Run(t.Context(), repo, Options{Ref: "nope"}); err == nil {
		t.Fatal("want error for bad ref")
	}
	repo = newRepo(t, "bogus: 1\n", false)
	if _, err := Run(context.Background(), repo, Options{}); err == nil {
		t.Fatal("want config error")
	}
}

func TestAuditFindings(t *testing.T) {
	rep := audit.Report{Obligations: []audit.Obligation{
		{ID: "a", Level: audit.Required},
		{ID: "b", Level: audit.Recommended},
		{ID: "c", Level: audit.Required, Waived: true},
		{ID: "d", Level: audit.Required, Satisfied: true},
	}}
	fs := rep.Findings()
	if len(fs) != 2 || fs[0].Rule != "setup:a" || fs[0].Severity != state.SeverityBlock || fs[1].Severity != state.SeverityNote {
		t.Fatalf("%+v", fs)
	}
}

// argLogPrek installs a fake prek that records its argv, one call per line, in the returned file.
func argLogPrek(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	logf := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("PREK_ARGS_LOG", logf)
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\nif [ \"$1\" = --version ]; then echo 'prek 0.0.0'; exit 0; fi\necho \"$*\" >> \"$PREK_ARGS_LOG\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte(script), 0o700); err != nil { // #nosec G306 -- fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logf
}

func TestFastPassesWorkingTreeChangesToPrek(t *testing.T) {
	logf := argLogPrek(t)
	repo := newRepo(t, head, true)
	put(t, repo.Root, "a.go", "package a\n\nvar X = 2\n") // unstaged modification
	put(t, repo.Root, "new.go", "package a\n")            // untracked
	put(t, repo.Root, "gone.go", "package a\n")
	git(t, repo.Root, "add", "gone.go")
	git(t, repo.Root, "commit", "-qm", "gone")
	if err := os.Remove(filepath.Join(repo.Root, "gone.go")); err != nil {
		t.Fatal(err)
	}
	out, err := Run(t.Context(), repo, Options{Fast: true})
	if err != nil || out.Record.Status != "pass" {
		t.Fatalf("%+v %v", out, err)
	}
	b, _ := os.ReadFile(logf) // #nosec G304 -- test-owned log path // #nosec G304 -- test-owned temp path
	got := strings.TrimSpace(string(b))
	if !strings.HasSuffix(got, "--files a.go new.go") || strings.Contains(got, "gone.go") || strings.Contains(got, "--all-files") {
		t.Fatalf("prek args: %q", got)
	}
	if _, err := state.Open(repo.CommonDir).LoadCheck(out.Record.SHA); err == nil {
		t.Fatal("fast mode wrote a record")
	}
}

func TestFastSkipsPrekWithoutChanges(t *testing.T) {
	logf := argLogPrek(t)
	repo := newRepo(t, head, true)
	out, err := Run(t.Context(), repo, Options{Fast: true})
	if err != nil || out.Record.Status != "pass" || len(out.Record.Steps) != 1 || out.Record.Steps[0].Status != state.StatusSkipped {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := os.Stat(logf); err == nil {
		t.Fatal("prek ran with no changed files")
	}
	if len(out.Notes) == 0 || !strings.Contains(strings.Join(out.Notes, "\n"), "skipped") {
		t.Fatalf("notes %q", out.Notes)
	}
}

func TestFastSlowTierIsANoteNotAFailure(t *testing.T) {
	argLogPrek(t)
	old := fastTierBudget
	fastTierBudget = -1
	t.Cleanup(func() { fastTierBudget = old })
	repo := newRepo(t, head, true)
	put(t, repo.Root, "new.go", "package a\n")
	out, err := Run(t.Context(), repo, Options{Fast: true})
	if err != nil || out.Record.Status != "pass" {
		t.Fatalf("%+v %v", out, err)
	}
	var found bool
	for _, f := range out.Record.Findings {
		if f.ID == "check:budget:pre-commit" {
			found = f.Severity == state.SeverityNote && f.Source == state.SourceCheck && strings.Contains(f.Message, "§4.5")
		}
	}
	if !found {
		t.Fatalf("%+v", out.Record.Findings)
	}
}

// Sequential runs on different commits share one path; stale files are cleaned.
func TestWorktreeReusedAcrossRuns(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, "prepare:\n  - {name: deps, run: [sh, -c, \"touch stale; mkdir -p node_modules; touch node_modules/keep; cat a.go > ../seen-$(git rev-parse --short HEAD)\"]}\n", true)
	if _, err := Run(t.Context(), repo, Options{}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(repo.CommonDir, "verify", "worktrees", "check")
	if _, err := os.Stat(filepath.Join(wt, "stale")); err != nil {
		t.Fatal("stale marker should exist after run 1")
	}
	if err := os.WriteFile(filepath.Join(wt, "leftover"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	put(t, repo.Root, "second.txt", "2\n")
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "second")
	// prepare recreates stale, so assert on leftover (untracked, not made by prepare).
	if _, err := Run(t.Context(), repo, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "second.txt")); err != nil {
		t.Fatalf("second commit's file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "leftover")); err == nil {
		t.Fatal("leftover untracked file survived")
	}
	if _, err := os.Stat(filepath.Join(wt, "node_modules", "keep")); err != nil {
		t.Fatalf("node_modules not kept: %v", err)
	}
	ents, _ := os.ReadDir(filepath.Join(repo.CommonDir, "verify", "worktrees"))
	n := 0
	for _, e := range ents {
		if e.IsDir() {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one worktree dir, got %v", ents)
	}
}

func TestConcurrentChecksSerialise(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, "prepare:\n  - {name: deps, run: [sh, -c, \"sleep 0.2\"]}\n", true)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			out, err := Run(t.Context(), repo, Options{})
			if err == nil && out.Record.Status != "pass" {
				err = fmt.Errorf("status %s: %+v", out.Record.Status, out.Record.Findings)
			}
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrekScopeArgs(t *testing.T) {
	logf := argLogPrek(t)
	repo := newRepo(t, head, true)
	out, err := Run(t.Context(), repo, Options{})
	if err != nil || out.Record.Scope != ScopeAffected || out.Record.ScopeReason != "" {
		t.Fatalf("%+v %v", out.Record, err)
	}
	b, _ := os.ReadFile(logf) // #nosec G304 -- test-owned log path // #nosec G304 -- test-owned temp path
	for _, st := range []string{"pre-commit", "pre-push"} {
		want := "run --from-ref " + out.Record.MergeBase + " --to-ref " + out.Record.SHA + " --stage " + st + " --no-progress --color never"
		if !strings.Contains(string(b), want) || strings.Contains(string(b), "--all-files") {
			t.Fatalf("want %q in %q", want, b)
		}
	}

	// A manifest change makes the run full.
	if err := os.Remove(logf); err != nil {
		t.Fatal(err)
	}
	put(t, repo.Root, "go.mod", "module x\n")
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "mod")
	out, err = Run(t.Context(), repo, Options{})
	if err != nil || out.Record.Scope != ScopeFull || !strings.Contains(out.Record.ScopeReason, "go.mod") {
		t.Fatalf("%+v %v", out.Record, err)
	}
	b, _ = os.ReadFile(logf) // #nosec G304 -- test-owned log path // #nosec G304 -- test-owned temp path
	if strings.Contains(string(b), "--from-ref") || strings.Count(string(b), "--all-files") != 2 {
		t.Fatalf("full args: %q", b)
	}
	loaded, err := state.Open(repo.CommonDir).LoadCheck(out.Record.SHA)
	if err != nil || loaded.Scope != ScopeFull {
		t.Fatalf("%+v %v", loaded, err)
	}
}

func TestFastGuardsWorkingTreeIncludingUntracked(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, head, true)
	put(t, repo.Root, "new.go", "package a\nfunc TestNew(t *testing.T) { t."+"Skip() }\n")
	out, err := Run(t.Context(), repo, Options{Fast: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(out.Record.Findings, func(f state.Finding) bool { return f.Rule == "guard:suppressions" && f.Path == "new.go" }) {
		t.Fatalf("untracked guard bypass: %+v", out.Record.Findings)
	}
	if _, err := os.Stat(filepath.Join(repo.CommonDir, "verify", "runs", out.Record.SHA, "check.json")); !os.IsNotExist(err) {
		t.Fatalf("fast mode wrote state evidence: %v", err)
	}
}

func TestPrepareTrackedMutationCannotPass(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, "prepare:\n  - {name: rewrite, run: [sh, -c, \"echo changed > a.go\"]}\n", true)
	if _, err := Run(t.Context(), repo, Options{}); err == nil || !strings.Contains(err.Error(), "tracked worktree/index differs") {
		t.Fatalf("prepare mutation accepted: %v", err)
	}
}

func TestFullStageTrackedMutationCannotPass(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, head, true)
	// Stage wrappers in the repository worktree are test fixtures, not evidence.
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\nif [ \"$1\" = run ]; then echo modified > a.go; fi\n"
	// #nosec G306 -- Fake prek fixture must be executable.
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := Run(t.Context(), repo, Options{}); err == nil || !strings.Contains(err.Error(), "tracked worktree/index differs") || !strings.Contains(err.Error(), "a.go") {
		t.Fatalf("stage mutation accepted or unnamed: %v", err)
	}
}

func TestIncompleteCleanupQuarantinesWorktreeAcrossRuns(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, head, true)
	store := state.Open(repo.CommonDir)
	wt := filepath.Join(store.WorktreesDir(), "check")
	sha, err := repo.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ResetWorktree(t.Context(), wt, sha, nil); err != nil {
		t.Fatal(err)
	}
	release, err := gitx.BeginWorktreeUse(wt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), repo, Options{}); err == nil || !strings.Contains(err.Error(), "marker") {
		t.Fatalf("quarantined worktree reused: %v", err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), repo, Options{}); err != nil {
		t.Fatalf("explicit recovery rejected: %v", err)
	}
}

func TestCheckLogWriteFailureFailsClosed(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, head, true)
	sha, err := repo.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(repo.CommonDir, "verify", "runs", sha, "logs")
	if err = os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A non-directory logs destination must not silently drop command evidence.
	if err = os.Remove(logDir); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(logDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), repo, Options{}); err == nil {
		t.Fatal("missing log accepted")
	}
}

func fakeSecretToken() string {
	return "gh" + "p_" + "aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5"
}

func TestBlockingGuardSkipsHooks(t *testing.T) {
	logf := argLogPrek(t)
	repo := newRepo(t, "prepare:\n  - {name: deps, run: [sh, -c, \"touch prepared\"]}\n", true)
	put(t, repo.Root, "cfg.txt", "token = "+fakeSecretToken()+"\n")
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "add secret")
	out, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := out.Record
	var names []string
	for _, s := range rec.Steps {
		if s.Status != state.StatusSkipped || s.DurationMS != 0 {
			t.Fatalf("step not skipped: %+v", s)
		}
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"prepare-deps", "prek-pre-commit", "prek-pre-push"}) {
		t.Fatalf("planned steps %v", names)
	}
	if rec.Status != state.StatusFail || !slices.ContainsFunc(rec.Findings, func(f state.Finding) bool { return f.Rule == "guard:secrets" }) {
		t.Fatalf("%+v", rec)
	}
	if !slices.Contains(out.Notes, "guards blocked; hooks skipped") {
		t.Fatalf("notes %v", out.Notes)
	}
	if _, err := os.Stat(logf); !os.IsNotExist(err) {
		t.Fatalf("prek was invoked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.CommonDir, "verify", "runs", rec.SHA, "check.json")); err != nil {
		t.Fatalf("record not saved: %v", err)
	}
}

func TestNonBlockingGuardNoteStillRunsHooks(t *testing.T) {
	logf := argLogPrek(t)
	repo := newRepo(t, head, true)
	put(t, repo.Root, ".golangci.yml", "version: \"2\"\n")
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "quality config")
	out, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(out.Record.Findings, func(f state.Finding) bool { return f.Rule == "guard:config" }) {
		t.Fatalf("no config note: %+v", out.Record.Findings)
	}
	b, rerr := os.ReadFile(logf) // #nosec G304 -- test-owned log path
	if rerr != nil || !strings.Contains(string(b), "--stage pre-commit") {
		t.Fatalf("prek not run: %q %v", b, rerr)
	}
	for _, s := range out.Record.Steps {
		if s.Status != state.StatusPass {
			t.Fatalf("step %+v", s)
		}
	}
}

func TestAcceptedGuardFindingLetsHooksRun(t *testing.T) {
	logf := argLogPrek(t)
	repo := newRepo(t, head, true)
	put(t, repo.Root, "cfg.txt", "token = "+fakeSecretToken()+"\n")
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "add secret")
	first, err := Run(t.Context(), repo, Options{})
	if err != nil || first.Record.Status != state.StatusFail {
		t.Fatalf("%+v %v", first, err)
	}
	var id string
	for _, f := range first.Record.Findings {
		if f.Rule == "guard:secrets" {
			id = f.ID
		}
	}
	err = state.Open(repo.CommonDir).UpdateBranch(t.Context(), "feature", func(b *state.Branch) error {
		b.Decisions = append(b.Decisions, state.Decision{Kind: state.DecideAccept, Target: id, Reason: "fixture", SHA: first.Record.SHA, Time: time.Now().UTC()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, rerr := os.ReadFile(logf) // #nosec G304 -- test-owned log path
	if rerr != nil || !strings.Contains(string(b), "--stage pre-push") {
		t.Fatalf("hooks did not run after accept: %q %v", b, rerr)
	}
	if len(second.Record.Steps) != 2 || second.Record.Steps[0].Status != state.StatusPass {
		t.Fatalf("%+v", second.Record.Steps)
	}
}

const (
	lintFailure  = "lint things.....................................................................................Failed\n- hook id: lint\n- duration: 0.40s\n- exit code: 2\n\n  a.go:3: bad thing\n  second line\n"
	fixerFailure = "fix eof.........................................................................................Failed\n- hook id: end-of-file-fixer\n- duration: 0.01s\n- files were modified by this hook\n"
	slowHooks    = "ok hook.........................................................................................Passed\n- hook id: ok-hook\n- duration: 0.32s\n\nvet.............................................................................................Passed\n- hook id: vet\n- duration: 1.50s\n"
)

// stageSpec scripts what the fake prek does for one stage: run sh in the
// worktree, print out, then exit with code.
type stageSpec struct {
	sh, out string
	code    int
}

func stagePrek(t *testing.T, specs map[string]stageSpec) {
	t.Helper()
	dir := t.TempDir()
	for stage, sp := range specs {
		for ext, content := range map[string]string{"sh": sp.sh, "out": sp.out, "exit": fmt.Sprint(sp.code)} {
			if err := os.WriteFile(filepath.Join(dir, stage+"."+ext), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PREK_FAKE_DIR", dir)
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\nstage=\nwhile [ $# -gt 0 ]; do [ \"$1\" = --stage ] && stage=$2; shift; done\nd=\"$PREK_FAKE_DIR\"\n[ -f \"$d/$stage.sh\" ] && sh \"$d/$stage.sh\"\n[ -f \"$d/$stage.out\" ] && cat \"$d/$stage.out\"\n[ -f \"$d/$stage.exit\" ] && exit \"$(cat \"$d/$stage.exit\")\"\nexit 0\n"
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte(script), 0o700); err != nil { // #nosec G306 -- fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func finding(t *testing.T, rec *state.CheckRecord, id string) state.Finding {
	t.Helper()
	for _, f := range rec.Findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("no finding %s in %+v", id, rec.Findings)
	return state.Finding{}
}

func stepStatuses(rec *state.CheckRecord) []string {
	var out []string
	for _, s := range rec.Steps {
		out = append(out, s.Name+"="+s.Status)
	}
	return out
}

func TestFailedHookIsOneFindingWithExcerptAndLog(t *testing.T) {
	stagePrek(t, map[string]stageSpec{"pre-commit": {out: lintFailure, code: 1}})
	repo := newRepo(t, head, true)
	out, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := finding(t, out.Record, "check:pre-commit:lint")
	if f.Severity != state.SeverityBlock || f.Hook != "lint" || f.Message != "lint failed (exit 2): a.go:3: bad thing" || f.Excerpt != "a.go:3: bad thing\nsecond line" {
		t.Fatalf("%+v", f)
	}
	if _, err := os.Stat(f.Log); err != nil || f.Log != filepath.Join(repo.CommonDir, "verify", "runs", out.Record.SHA, "logs", "1", "prek-pre-commit.log") {
		t.Fatalf("log %q (%v)", f.Log, err)
	}
	var blocking []string
	for _, x := range out.Record.Findings {
		if x.Blocking() {
			blocking = append(blocking, x.ID)
		}
	}
	if !slices.Equal(blocking, []string{"check:pre-commit:lint"}) {
		t.Fatalf("blocking findings %v", blocking)
	}
	want := []string{"prek-pre-commit=fail", "prek-pre-push=skipped"}
	if got := stepStatuses(out.Record); !slices.Equal(got, want) {
		t.Fatalf("steps %v want %v", got, want)
	}
}

func TestDuplicateHookIDsEachGetAFinding(t *testing.T) {
	twice := "lint a.....................................................................................Failed\n- hook id: lint\n- duration: 0.10s\n- exit code: 1\n\n  first.go:1: one\n" +
		"lint b.....................................................................................Failed\n- hook id: lint\n- duration: 0.10s\n- exit code: 1\n\n  second.go:2: two\n"
	stagePrek(t, map[string]stageSpec{"pre-commit": {out: twice, code: 1}})
	out, err := Run(t.Context(), newRepo(t, head, true), Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, second := finding(t, out.Record, "check:pre-commit:lint"), finding(t, out.Record, "check:pre-commit:lint#2")
	if first.Excerpt != "first.go:1: one" || second.Excerpt != "second.go:2: two" {
		t.Fatalf("excerpts %q and %q", first.Excerpt, second.Excerpt)
	}
}

func TestPrekErrorKeepsStageFindingWithExcerpt(t *testing.T) {
	stagePrek(t, map[string]stageSpec{"pre-commit": {out: "error: Failed to parse `.pre-commit-config.yaml`\n", code: 2}})
	out, err := Run(t.Context(), newRepo(t, head, true), Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := finding(t, out.Record, "check:prek-pre-commit")
	if f.Hook != "" || !strings.Contains(f.Excerpt, "Failed to parse") || f.Log == "" {
		t.Fatalf("%+v", f)
	}
}

func TestFixerRewriteIsAFindingNotAnAbort(t *testing.T) {
	stagePrek(t, map[string]stageSpec{"pre-commit": {sh: "printf x >> a.go", out: fixerFailure, code: 1}})
	out, err := Run(t.Context(), newRepo(t, head, true), Options{})
	if err != nil {
		t.Fatalf("fixer rewrite aborted the check: %v", err)
	}
	f := finding(t, out.Record, "check:pre-commit:end-of-file-fixer")
	if f.Severity != state.SeverityBlock || f.Hook != "end-of-file-fixer" || f.Message != "modified a.go; run the fixer and commit" {
		t.Fatalf("%+v", f)
	}
	want := []string{"prek-pre-commit=fail", "prek-pre-push=skipped"}
	if got := stepStatuses(out.Record); !slices.Equal(got, want) || out.Record.Status != "fail" {
		t.Fatalf("steps %v want %v, status %s", got, want, out.Record.Status)
	}
}

func TestFixerWithNonzeroExitStillSaysModified(t *testing.T) {
	out := strings.Replace(fixerFailure, "- files were", "- exit code: 1\n- files were", 1)
	stagePrek(t, map[string]stageSpec{"pre-commit": {sh: "printf x >> a.go", out: out, code: 1}})
	res, err := Run(t.Context(), newRepo(t, head, true), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f := finding(t, res.Record, "check:pre-commit:end-of-file-fixer"); f.Message != "modified a.go; run the fixer and commit" {
		t.Fatalf("%+v", f)
	}
}

func TestFailedPrepareRecordsLaterStepsAsSkipped(t *testing.T) {
	fakePrek(t)
	out, err := Run(t.Context(), newRepo(t, "prepare:\n  - {name: deps, run: [sh, -c, \"echo boom >&2; exit 1\"]}\n", true), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prepare-deps=fail", "prek-pre-commit=skipped", "prek-pre-push=skipped"}
	if got := stepStatuses(out.Record); !slices.Equal(got, want) {
		t.Fatalf("steps %v want %v", got, want)
	}
	if s := out.Record.Steps[1]; s.DurationMS != 0 || s.ExitCode != 0 {
		t.Fatalf("skipped step %+v", s)
	}
	f := finding(t, out.Record, "check:prepare-deps")
	if !strings.Contains(f.Excerpt, "boom") || f.Log == "" {
		t.Fatalf("%+v", f)
	}
}

func TestEachAttemptKeepsItsOwnLogs(t *testing.T) {
	fakePrek(t)
	repo := newRepo(t, "prepare:\n  - {name: deps, run: [sh, -c, \"exit 1\"]}\n", true)
	sha, err := repo.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(repo.CommonDir, "verify", "runs", sha, "logs")
	for attempt := 1; attempt <= 2; attempt++ {
		out, err := Run(t.Context(), repo, Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(logs, fmt.Sprint(attempt))
		if out.Record.Attempt != attempt {
			t.Fatalf("attempt %d: record has attempt %d", attempt, out.Record.Attempt)
		}
		if _, err := os.Stat(filepath.Join(want, "prepare-deps.log")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(logs, "1", "prepare-deps.log")); err != nil {
		t.Fatalf("attempt 1 log overwritten or removed: %v", err)
	}
}

func TestStageOverBudgetAddsNoteNamingSlowestHooks(t *testing.T) {
	stagePrek(t, map[string]stageSpec{"pre-push": {out: slowHooks}})
	old := fullTierBudget
	fullTierBudget = -1
	t.Cleanup(func() { fullTierBudget = old })
	out, err := Run(t.Context(), newRepo(t, head, true), Options{})
	if err != nil || out.Record.Status != "pass" {
		t.Fatalf("%+v %v", out, err)
	}
	f := finding(t, out.Record, "check:budget:pre-push")
	if f.Severity != state.SeverityNote || !strings.Contains(f.Message, "vet 1.5s, ok-hook 0.3s") {
		t.Fatalf("%+v", f)
	}
}

// The captured fixtures cover the output format; this proves Run against the real binary.
func TestRealPrekFailureAndFixer(t *testing.T) {
	if _, err := exec.LookPath("prek"); err != nil {
		t.Skip("prek missing")
	}
	repo := newRepo(t, head, true)
	put(t, repo.Root, ".pre-commit-config.yaml", `repos:
  - repo: local
    hooks:
      - {id: lint, name: lint, entry: ./lint.sh, language: system, pass_filenames: false, always_run: true}
      - {id: end-of-file-fixer, name: fixer, entry: ./fix.sh, language: system, pass_filenames: false, always_run: true}
`)
	put(t, repo.Root, "lint.sh", "#!/bin/sh\necho 'a.go:3: bad thing'\nexit 2\n")
	put(t, repo.Root, "fix.sh", "#!/bin/sh\nprintf x >> a.go\n")
	for _, f := range []string{"lint.sh", "fix.sh"} {
		if err := os.Chmod(filepath.Join(repo.Root, f), 0o700); err != nil { // #nosec G302 -- test fixture script
			t.Fatal(err)
		}
	}
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "hooks")
	out, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f := finding(t, out.Record, "check:pre-commit:lint"); f.Message != "lint failed (exit 2): a.go:3: bad thing" {
		t.Fatalf("%+v", f)
	}
	if f := finding(t, out.Record, "check:pre-commit:end-of-file-fixer"); f.Message != "modified a.go; run the fixer and commit" {
		t.Fatalf("%+v", f)
	}
	if h := out.Record.Steps[0].Hooks; len(h) != 2 || h[0].ID != "lint" || h[0].Status != state.HookFail || h[1].Status != state.HookModified {
		t.Fatalf("%+v", h)
	}
}

// slowPrek logs each stage run (not list or version calls) to the returned file.
// While the returned flag file exists, every stage fails.
func slowPrek(t *testing.T) (calls, failFlag string) {
	t.Helper()
	calls = filepath.Join(t.TempDir(), "calls.log")
	failFlag = filepath.Join(t.TempDir(), "fail")
	t.Setenv("PREK_CALLS_LOG", calls)
	t.Setenv("PREK_FAIL_FLAG", failFlag)
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\nif [ \"$1\" = --version ]; then echo 'prek 0.5.5'; exit 0; fi\necho \"$*\" >> \"$PREK_CALLS_LOG\"\n[ -f \"$PREK_FAIL_FLAG\" ] && exit 1\nexit 0\n"
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte(script), 0o700); err != nil { // #nosec G306 -- fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls, failFlag
}

func stageRuns(t *testing.T, calls string) int {
	t.Helper()
	b, err := os.ReadFile(calls) // #nosec G304 -- test-owned temp path
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

func TestIdenticalTreeReusesPassingCheck(t *testing.T) {
	calls, _ := slowPrek(t)
	repo := newRepo(t, head, true)
	first, err := Run(t.Context(), repo, Options{})
	if err != nil || first.Record.Status != "pass" {
		t.Fatalf("%+v %v", first, err)
	}
	git(t, repo.Root, "commit", "-q", "--allow-empty", "-m", "same tree")
	second, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := second.Record
	if r.SHA == first.Record.SHA || r.ReusedFrom != first.Record.SHA || r.Status != "pass" || r.Tree != first.Record.Tree {
		t.Fatalf("%+v", r)
	}
	if !slices.Contains(second.Notes, "reused check from "+first.Record.SHA+" (identical tree)") {
		t.Fatalf("notes %q", second.Notes)
	}
	if n := stageRuns(t, calls); n != 2 {
		t.Fatalf("prek ran %d stages, want 2 (pre-commit and pre-push once)", n)
	}
	saved, err := state.Open(repo.CommonDir).LoadCheck(r.SHA)
	if err != nil || saved.ReusedFrom != first.Record.SHA {
		t.Fatalf("%+v %v", saved, err)
	}
	idx, err := os.ReadFile(filepath.Join(repo.CommonDir, "verify", "trees", r.Tree+".json")) // #nosec G304 -- test-owned state path
	if err != nil || !strings.Contains(string(idx), `"schema": "verify.tree/1"`) || !strings.Contains(string(idx), `"sha": "`+r.SHA+`"`) {
		t.Fatalf("tree index %s %v", idx, err)
	}
}

func TestDifferentTreeIsNotReused(t *testing.T) {
	calls, _ := slowPrek(t)
	repo := newRepo(t, head, true)
	if _, err := Run(t.Context(), repo, Options{}); err != nil {
		t.Fatal(err)
	}
	put(t, repo.Root, "b.go", "package a\n")
	git(t, repo.Root, "add", ".")
	git(t, repo.Root, "commit", "-qm", "new tree")
	out, err := Run(t.Context(), repo, Options{})
	if err != nil || out.Record.ReusedFrom != "" || stageRuns(t, calls) != 4 {
		t.Fatalf("%+v %v, stage runs %d", out.Record, err, stageRuns(t, calls))
	}
}

func TestFailedCheckIsNotReused(t *testing.T) {
	calls, failFlag := slowPrek(t)
	repo := newRepo(t, head, true)
	put(t, filepath.Dir(failFlag), "fail", "")
	first, err := Run(t.Context(), repo, Options{})
	if err != nil || first.Record.Status != "fail" {
		t.Fatalf("%+v %v", first, err)
	}
	if err = os.Remove(failFlag); err != nil {
		t.Fatal(err)
	}
	git(t, repo.Root, "commit", "-q", "--allow-empty", "-m", "same tree")
	second, err := Run(t.Context(), repo, Options{})
	if err != nil || second.Record.ReusedFrom != "" || second.Record.Status != "pass" || stageRuns(t, calls) != 3 {
		t.Fatalf("%+v %v, stage runs %d", second.Record, err, stageRuns(t, calls))
	}
}

func TestDifferentScopeIsNotReused(t *testing.T) {
	calls, _ := slowPrek(t)
	repo := newRepo(t, head, true)
	first, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	store := state.Open(repo.CommonDir)
	first.Record.Scope, first.Record.ScopeReason = ScopeFull, "recorded under another scope"
	if err = store.SaveCheck(t.Context(), first.Record); err != nil {
		t.Fatal(err)
	}
	git(t, repo.Root, "commit", "-q", "--allow-empty", "-m", "same tree")
	second, err := Run(t.Context(), repo, Options{})
	if err != nil || second.Record.ReusedFrom != "" || second.Record.Scope != ScopeAffected || stageRuns(t, calls) != 4 {
		t.Fatalf("%+v %v, stage runs %d", second.Record, err, stageRuns(t, calls))
	}
}

func TestSameShaRecheckReturnsThePassedRecordUnchanged(t *testing.T) {
	calls, _ := slowPrek(t)
	repo := newRepo(t, head, true)
	first, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo.CommonDir, "verify", "runs", first.Record.SHA, "check.json")
	before, err := os.ReadFile(path) // #nosec G304 -- test-owned state path
	if err != nil {
		t.Fatal(err)
	}
	again, err := Run(t.Context(), repo, Options{})
	if err != nil || again.Record.SHA != first.Record.SHA || again.Record.ReusedFrom != "" || stageRuns(t, calls) != 2 {
		t.Fatalf("%+v %v, stage runs %d", again.Record, err, stageRuns(t, calls))
	}
	if !slices.Contains(again.Notes, "check already passed for this commit") {
		t.Fatalf("notes %q", again.Notes)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) { // #nosec G304 -- test-owned state path
		t.Fatalf("record rewritten: %v\n%s\n%s", err, before, after)
	}
}

func TestSameShaRecheckOfAReusedRecordSaysAlreadyPassed(t *testing.T) {
	slowPrek(t)
	repo := newRepo(t, head, true)
	first, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	git(t, repo.Root, "commit", "-q", "--allow-empty", "-m", "same tree")
	reused, err := Run(t.Context(), repo, Options{})
	if err != nil || reused.Record.ReusedFrom != first.Record.SHA {
		t.Fatalf("%+v %v", reused.Record, err)
	}
	path := filepath.Join(repo.CommonDir, "verify", "runs", reused.Record.SHA, "check.json")
	before, err := os.ReadFile(path) // #nosec G304 -- test-owned state path
	if err != nil {
		t.Fatal(err)
	}
	again, err := Run(t.Context(), repo, Options{})
	if err != nil || again.Record.SHA != reused.Record.SHA || again.Record.ReusedFrom != first.Record.SHA {
		t.Fatalf("%+v %v", again.Record, err)
	}
	if !slices.Contains(again.Notes, "check already passed for this commit") || slices.ContainsFunc(again.Notes, func(n string) bool { return strings.HasPrefix(n, "reused check from") }) {
		t.Fatalf("notes %q", again.Notes)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) { // #nosec G304 -- test-owned state path
		t.Fatalf("record rewritten: %v\n%s\n%s", err, before, after)
	}
}

func TestOldPassIsNotReused(t *testing.T) {
	calls, _ := slowPrek(t)
	repo := newRepo(t, head, true)
	first, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	first.Record.Started, first.Record.Finished = time.Now().Add(-26*time.Hour), time.Now().Add(-25*time.Hour)
	if err = state.Open(repo.CommonDir).SaveCheck(t.Context(), first.Record); err != nil {
		t.Fatal(err)
	}
	git(t, repo.Root, "commit", "-q", "--allow-empty", "-m", "same tree")
	second, err := Run(t.Context(), repo, Options{})
	if err != nil || second.Record.ReusedFrom != "" || stageRuns(t, calls) != 4 {
		t.Fatalf("%+v %v, stage runs %d", second.Record, err, stageRuns(t, calls))
	}
}

func TestReuseStillRunsGuardsOnTheNewHistory(t *testing.T) {
	calls, _ := slowPrek(t)
	repo := newRepo(t, head, true)
	if first, err := Run(t.Context(), repo, Options{}); err != nil || first.Record.Status != "pass" {
		t.Fatalf("%+v %v", first, err)
	}
	git(t, repo.Root, "checkout", "-q", "-b", "leaky", "main")
	put(t, repo.Root, "a.go", "package a\n\n// token = "+"gh"+"p_"+"aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5\n")
	git(t, repo.Root, "commit", "-qam", "add a token")
	put(t, repo.Root, "a.go", "package a\n\nvar X = 1\n")
	git(t, repo.Root, "commit", "-qam", "remove it again")
	second, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := second.Record
	if r.Status != "fail" || !slices.ContainsFunc(r.Findings, func(f state.Finding) bool { return strings.HasPrefix(f.ID, "guard:secrets") }) {
		t.Fatalf("status %s, findings %+v", r.Status, r.Findings)
	}
	if n := stageRuns(t, calls); n != 2 {
		t.Fatalf("prek ran %d stages, want only the first branch's 2", n)
	}
}

func TestQueuedCheckOnSameShaReusesThePassTheLockHolderRecorded(t *testing.T) {
	calls, _ := slowPrek(t)
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\nif [ \"$1\" = --version ]; then echo 'prek 0.5.5'; exit 0; fi\necho \"$*\" >> \"$PREK_CALLS_LOG\"\nsleep 1\nexit 0\n"
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "prek"), []byte(script), 0o700); err != nil { // #nosec G306 -- fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := newRepo(t, head, true)
	type result struct {
		out *Outcome
		err error
	}
	holder := make(chan result, 1)
	go func() {
		out, err := Run(t.Context(), repo, Options{})
		holder <- result{out, err}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for stageRuns(t, calls) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first check never reached its hooks")
		}
		time.Sleep(20 * time.Millisecond)
	}
	queued, err := Run(t.Context(), repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	first := <-holder
	if first.err != nil || first.out.Record.Status != "pass" {
		t.Fatalf("%+v %v", first.out, first.err)
	}
	if queued.Record.Status != "pass" || queued.Record.SHA != first.out.Record.SHA {
		t.Fatalf("%+v", queued.Record)
	}
	if !slices.Contains(queued.Notes, "check already passed for this commit") {
		t.Fatalf("notes %q", queued.Notes)
	}
	if n := stageRuns(t, calls); n != 2 {
		t.Fatalf("prek ran %d stages, want 2 (the lock holder's only)", n)
	}
}
