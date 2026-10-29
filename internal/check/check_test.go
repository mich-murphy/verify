package check

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	if err != nil || out.Record.Status != "fail" || len(out.Record.Steps) != 1 {
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
	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[]'; exit 0; fi\necho \"$*\" >> \"$PREK_ARGS_LOG\"\nexit 0\n"
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
	b, _ := os.ReadFile(logf) // #nosec G304 -- test-owned temp path
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
		if f.ID == "check:fast-tier-slow" {
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
	b, _ := os.ReadFile(logf) // #nosec G304 -- test-owned temp path
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
	b, _ = os.ReadFile(logf) // #nosec G304 -- test-owned temp path
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
	if _, err := Run(t.Context(), repo, Options{}); err == nil || !strings.Contains(err.Error(), "tracked worktree/index differs") {
		t.Fatalf("stage mutation accepted: %v", err)
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
