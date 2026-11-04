package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/state"
)

const fakeGHScript = `#!/bin/sh
echo "$*" >> "$GH_LOG"
case "$1 $2" in
"pr view") echo '{"number":7,"url":"https://example.test/pr/7","state":"OPEN"}'; exit 0 ;;
esac
exit 0
`

type shipEnv struct {
	repo, origin, sha, ghLog string
	st                       *state.Store
}

func newShipEnv(t *testing.T) *shipEnv {
	t.Helper()
	repo := setup(t, "none")
	commit(t, repo, "a.go", "package a\n\nvar X = 1\n")
	bin := os.Getenv("PATH")
	write(t, bin, "gh", fakeGHScript)
	if err := os.Chmod(filepath.Join(bin, "gh"), 0o700); err != nil { //nolint:gosec // fake executable in a temp dir
		t.Fatal(err)
	}
	logf := filepath.Join(bin, "gh.log")
	t.Setenv("GH_LOG", logf)
	origin, _ := filepath.EvalSymlinks(t.TempDir())
	gitRun(t, origin, "init", "-q", "--bare", "-b", "main")
	gitRun(t, repo, "remote", "add", "origin", "git@github.com:example/project.git")
	ssh := filepath.Join(bin, "ssh")
	write(t, bin, "ssh", "#!/bin/sh\ncase \"$*\" in *git-upload-pack*) exec git-upload-pack \"$VERIFY_BARE\";; *git-receive-pack*) exec git-receive-pack \"$VERIFY_BARE\";; esac\nexit 1\n")
	if err := os.Chmod(ssh, 0o700); err != nil { // #nosec G302,G703 -- fake executable in test temp directory
		t.Fatal(err)
	}
	t.Setenv("VERIFY_BARE", origin)
	r, _ := runCLI(t, "status")
	return &shipEnv{repo: repo, origin: origin, sha: r.SHA, ghLog: logf, st: state.Open(filepath.Join(repo, ".git"))}
}

func (e *shipEnv) signoff(t *testing.T) {
	t.Helper()
	commit(t, e.repo, "verify.yaml", baseYAML+"signoff_paths: [\"a.go\"]\n")
	e.sha = strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "HEAD"))
}

func (e *shipEnv) records(t *testing.T, lane string, review bool) {
	t.Helper()
	base := strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "main"))
	now := time.Now().UTC()
	if err := e.st.SaveCheck(t.Context(), &state.CheckRecord{SHA: e.sha, MergeBase: base, Lane: lane, Scope: "affected", Status: "pass", Started: now, Finished: now}); err != nil {
		t.Fatal(err)
	}
	if review {
		if err := e.st.SaveReview(t.Context(), &state.ReviewRecord{SHA: e.sha, MergeBase: base, Branch: "feature", Reviewer: "codex", Status: "pass", CallsUsed: 1, Attempts: 1, Finished: now}); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *shipEnv) ghCalls() string {
	b, _ := os.ReadFile(e.ghLog)
	return string(b)
}

func TestShipRefusals(t *testing.T) {
	e := newShipEnv(t)
	r, code := runCLI(t, "ship")
	if code != ExitBlocked || r.NextAction != "run verify check" {
		t.Fatalf("no check: %d %+v", code, r)
	}
	e.records(t, "standard", false)
	if r, code = runCLI(t, "ship"); code != ExitBlocked || r.NextAction != "run verify review" {
		t.Fatalf("no review: %d %+v", code, r)
	}
	e.signoff(t)
	e.records(t, "signoff", true)
	if r, code = runCLI(t, "ship"); code != ExitBlocked || !strings.Contains(r.NextAction, "decide approve") {
		t.Fatalf("no approval: %d %+v", code, r)
	}
	e.records(t, "signoff", true)
	if _, code := runCLI(t, "decide", "approve", "--reason", "fixture"); code != 0 {
		t.Fatalf("approve: %d", code)
	}
	write(t, e.repo, "dirty.txt", "x")
	if r, code = runCLI(t, "ship"); code != ExitBlocked || !strings.Contains(r.Message, "not clean") {
		t.Fatalf("dirty: %d %+v", code, r)
	}
	if e.ghCalls() != "" {
		t.Fatal("gh was called on refusal")
	}
}

func TestShipStaleSHA(t *testing.T) {
	e := newShipEnv(t)
	e.records(t, "standard", true)
	commit(t, e.repo, "b.go", "package a\n\nvar Y = 1\n")
	if r, code := runCLI(t, "ship"); code != ExitBlocked || r.NextAction != "run verify check" {
		t.Fatalf("stale: %d %+v", code, r)
	}
}

func TestShipSuccessAndDryRun(t *testing.T) {
	e := newShipEnv(t)
	e.records(t, "standard", true)
	r, code := runCLI(t, "ship", "--dry-run")
	if code != 0 || r.Status != "dry-run" || len(r.Steps) != 3 || e.ghCalls() != "" {
		t.Fatalf("dry: %d %+v", code, r)
	}
	r, code = runCLI(t, "ship")
	if code != 0 || r.Status != "shipped" {
		t.Fatalf("ship: %d %+v", code, r)
	}
	calls := e.ghCalls()
	for _, want := range []string{"pr edit 7 --repo github.com/example/project --body-file", "statuses/" + e.sha, "context=verify/verified"} {
		if !strings.Contains(calls, want) {
			t.Errorf("gh calls lack %q:\n%s", want, calls)
		}
	}
	if got := strings.TrimSpace(gitOut(t, e.origin, "rev-parse", "refs/heads/feature")); got != e.sha {
		t.Fatalf("remote ref: %q", got)
	}
}

func TestShipUsageAndFailure(t *testing.T) {
	e := newShipEnv(t)
	e.records(t, "standard", true)
	var sink strings.Builder
	if code := Run([]string{"ship", "extra"}, nil, &sink, &sink); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
	r, code := runCLI(t, "ship", "--remote", "nowhere")
	if code != ExitBlocked || !strings.Contains(r.Message, "remaining") || r.NextAction == "" {
		t.Fatalf("%d %+v", code, r)
	}
}

func TestDecideAndStatus(t *testing.T) {
	e := newShipEnv(t)
	e.signoff(t)
	e.records(t, "signoff", true)
	if _, code := runCLI(t, "decide", "approve"); code != ExitUsage {
		t.Fatalf("reason required: %d", code)
	}
	if _, code := runCLI(t, "decide", "accept", "F9", "--reason", "x"); code != ExitUsage {
		t.Fatalf("unknown id: %d", code)
	}
	if _, code := runCLI(t, "decide", "bogus", "--reason", "x"); code != ExitUsage {
		t.Fatalf("bad kind: %d", code)
	}
	if err := e.st.UpdateBranch(t.Context(), "feature", func(b *state.Branch) error {
		b.Findings = append(b.Findings, state.Finding{ID: "F1", Source: "review", Severity: "block", State: "open", Message: "bug"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s, _ := runCLI(t, "status")
	if !strings.Contains(s.Message, "eligible: no") || !strings.Contains(s.Message, "blocking: F1") {
		t.Fatalf("%+v", s)
	}
	if _, code := runCLI(t, "decide", "accept", "F1", "--reason", "fine"); code != 0 {
		t.Fatalf("accept: %d", code)
	}
	if _, code := runCLI(t, "decide", "--reason", "ok", "approve"); code != 0 {
		t.Fatalf("approve: %d", code)
	}
	s, _ = runCLI(t, "status")
	if !strings.Contains(s.Message, "eligible: yes") || !strings.Contains(s.Message, "decision: approve") || s.NextAction != "run verify ship" {
		t.Fatalf("%+v", s)
	}
	b, _ := e.st.LoadBranch("feature")
	if b.Findings[0].State != "accepted" || len(b.Decisions) != 2 {
		t.Fatalf("%+v", b)
	}
}

func TestDecideRejectsMismatchedLaneCheck(t *testing.T) {
	e := newShipEnv(t)
	base := strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "main"))
	now := time.Now().UTC()
	check := &state.CheckRecord{SHA: e.sha, MergeBase: base, Lane: "docs", Scope: "affected", Status: "fail", Started: now, Finished: now, Findings: []state.Finding{
		{ID: "setup:x", Source: state.SourceSetup, Severity: state.SeverityBlock, State: state.StateOpen, Message: "missing setup"},
	}}
	if err := e.st.SaveCheck(t.Context(), check); err != nil {
		t.Fatal(err)
	}
	if r, code := runCLI(t, "decide", "waive", "x", "--reason", "fixture"); code != ExitBlocked || r.NextAction != "run verify check" || r.Message != "no check record for HEAD" {
		t.Fatalf("stale docs-lane check must not authorize a waiver: code %d: %+v", code, r)
	}
	branch, err := e.st.LoadBranch("feature")
	if err != nil || len(branch.Decisions) != 0 {
		t.Fatalf("stale check recorded a decision: branch %+v, err %v", branch, err)
	}
	check.Lane = "standard"
	if err := e.st.SaveCheck(t.Context(), check); err != nil {
		t.Fatal(err)
	}
	if r, code := runCLI(t, "decide", "waive", "x", "--reason", "fixture"); code != ExitOK {
		t.Fatalf("fresh standard-lane check must permit the waiver: code %d: %+v", code, r)
	}
}

func TestDecideWaive(t *testing.T) {
	e := newShipEnv(t)
	now := time.Now().UTC()
	base := strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "main"))
	if err := e.st.SaveCheck(t.Context(), &state.CheckRecord{SHA: e.sha, MergeBase: base, Lane: "standard", Scope: "affected", Status: "fail", Started: now, Finished: now, Findings: []state.Finding{
		{ID: "setup:x", Source: "setup", Severity: "block", State: "open", Message: "missing setup"},
		{ID: "check:t", Source: "check", Severity: "block", State: "open", Message: "failed step"},
		{ID: "guard:g", Source: "guard", Severity: "block", State: "open", Message: "guard hit"},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"waive", "nope"}, {"accept", "check:t"}, {"accept", "setup:x"},
	} {
		if r, code := runCLI(t, append([]string{"decide"}, append(args, "--reason", "r")...)...); code != ExitUsage {
			t.Errorf("%v: code %d: %+v", args, code, r)
		}
	}
	if r, code := runCLI(t, "decide", "waive", "verify", "--reason", "r"); code != ExitUsage {
		t.Fatalf("missing verify must not be waived: %d %+v", code, r)
	}
	for _, args := range [][]string{{"waive", "x"}, {"accept", "guard:g"}} {
		if r, code := runCLI(t, append([]string{"decide"}, append(args, "--reason", "r")...)...); code != ExitOK {
			t.Errorf("%v: code %d: %+v", args, code, r)
		}
	}
	if s, _ := runCLI(t, "status"); !strings.Contains(s.Message, "decision: waive setup:x") {
		t.Fatalf("%+v", s)
	}
	noGo := &state.VerifyRecord{SHA: e.sha, MergeBase: base, Verdict: "no-go", Finished: time.Now().UTC(), Findings: []state.Finding{{ID: "verify:x", Source: state.SourceVerify, Severity: state.SeverityBlock, State: state.StateOpen, Message: "assertion failed"}}}
	if err := e.st.SaveVerify(t.Context(), noGo); err != nil {
		t.Fatal(err)
	}
	if r, code := runCLI(t, "decide", "waive", "verify", "--reason", "cannot run"); code != ExitUsage {
		t.Fatalf("no-go cannot be waived: %d %+v", code, r)
	}
	noGo.Verdict = "inconclusive"
	noGo.Findings[0].ID = "verify:inconclusive:setup"
	noGo.Findings[0].Rule = "verify:inconclusive"
	if err := e.st.SaveVerify(t.Context(), noGo); err != nil {
		t.Fatal(err)
	}
	if r, code := runCLI(t, "decide", "waive", "verify", "--reason", "tool unavailable"); code != ExitOK {
		t.Fatalf("inconclusive can be waived: %d %+v", code, r)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestShipDryRunPrintsPlanBeforeRemoteError(t *testing.T) {
	e := newShipEnv(t)
	e.records(t, "standard", true)
	gitRun(t, e.repo, "remote", "set-url", "origin", e.origin)
	r, code := runCLI(t, "ship", "--dry-run")
	if code != ExitBlocked || r.Status != "blocked" {
		t.Fatalf("exit code and status are unchanged: %d %+v", code, r)
	}
	var at []int
	for _, want := range []string{
		"push planned: git push --force-with-lease=refs/heads/feature:<remote value> origin " + e.sha + ":refs/heads/feature",
		`pr planned: gh pr view feature --repo github.com/<owner/repo>, then gh pr create (title "change a.go", base main, description "change a.go") or gh pr edit`,
		"status planned: gh api --hostname github.com -X POST repos/<owner/repo>/statuses/" + e.sha + " -f state=success -f context=verify/verified",
		"remote failed: selected remote is not a GitHub repository",
	} {
		i := strings.Index(r.Message, want)
		if i < 0 {
			t.Fatalf("message lacks %q:\n%s", want, r.Message)
		}
		at = append(at, i)
	}
	if !slices.IsSorted(at) {
		t.Fatalf("plan lines must precede the remote error: %v\n%s", at, r.Message)
	}
	if e.ghCalls() != "" {
		t.Fatal("dry run called gh")
	}
}

const createGHScript = `#!/bin/sh
echo "$*" >> "$GH_LOG"
case "$1 $2" in
"pr view") exit 1 ;;
"pr create")
	while [ $# -gt 0 ]; do [ "$1" = --body-file ] && /bin/cat "$2" > "$GH_BODY"; shift; done
	echo https://example.test/pr/9
	;;
esac
exit 0
`

// shipCreatesPR makes gh report no existing PR and keep the body it was given.
func (e *shipEnv) shipCreatesPR(t *testing.T) (body func() string) {
	t.Helper()
	bin := filepath.Dir(e.ghLog)
	write(t, bin, "gh", createGHScript)
	if err := os.Chmod(filepath.Join(bin, "gh"), 0o700); err != nil { //nolint:gosec // fake executable in a temp dir
		t.Fatal(err)
	}
	bodyFile := filepath.Join(bin, "body.md")
	t.Setenv("GH_BODY", bodyFile)
	return func() string {
		b, _ := os.ReadFile(bodyFile) //nolint:gosec // test path
		return string(b)
	}
}

const describeOut = `{"title":"Add the greeting","description":"Users now see a greeting on start. It exists so first runs feel welcome."}`

func TestShipOpensReadyPRWithGeneratedTitleAndDescription(t *testing.T) {
	e := newShipEnv(t)
	e.records(t, "standard", true)
	body := e.shipCreatesPR(t)
	fakeReviewer(t, describeOut)
	if r, code := runCLI(t, "ship"); code != 0 {
		t.Fatalf("ship: %d %+v", code, r)
	}
	create := ""
	for _, l := range strings.Split(e.ghCalls(), "\n") {
		if strings.HasPrefix(l, "pr create") {
			create = l
		}
	}
	if !strings.Contains(create, "--title Add the greeting --body-file") || strings.Contains(create, "--draft") {
		t.Fatalf("create args: %q", create)
	}
	if !strings.HasPrefix(body(), "Users now see a greeting on start. It exists so first runs feel welcome.\n\n## Verify\n") {
		t.Fatalf("body:\n%s", body())
	}
}

func TestShipFallsBackToCommitMessagesWhenReviewerFails(t *testing.T) {
	e := newShipEnv(t)
	commit(t, e.repo, "b.go", "package a\n\nvar Y = 1\n")
	e.sha = strings.TrimSpace(gitOut(t, e.repo, "rev-parse", "HEAD"))
	e.records(t, "standard", true)
	body := e.shipCreatesPR(t)
	fakeReviewer(t, `!{"is_error":true,"result":"Not logged in. Please run /login"}`)
	if r, code := runCLI(t, "ship"); code != 0 {
		t.Fatalf("ship: %d %+v", code, r)
	}
	if !strings.Contains(e.ghCalls(), "--title feature: change b.go --body-file") {
		t.Fatalf("title: %s", e.ghCalls())
	}
	want := "change a.go\nchange b.go\n\nDescription generated from commit messages (reviewer CLI unavailable)\n\n## Verify\n"
	if !strings.HasPrefix(body(), want) {
		t.Fatalf("body:\n%s", body())
	}
}

func TestShipRerunOnTheSameSHACallsNoReviewer(t *testing.T) {
	e := newShipEnv(t)
	e.records(t, "standard", true)
	e.shipCreatesPR(t)
	counter := fakeReviewer(t, describeOut)
	for range 2 {
		if r, code := runCLI(t, "ship"); code != 0 {
			t.Fatalf("ship: %d %+v", code, r)
		}
	}
	if n := calls(t, counter); n != 1 {
		t.Fatalf("reviewer invoked %d times for two ships of one SHA, want 1", n)
	}
	b := loadBranch(t, e.repo)
	if b.ReviewAttempts != 0 || b.Description == nil || b.Description.Source != "claude" || b.Description.SHA != e.sha {
		t.Fatalf("description must be cached without touching the review budget: %+v", b)
	}
}

func TestShipDryRunPrintsPlannedTitleWithoutCallingReviewer(t *testing.T) {
	e := newShipEnv(t)
	e.records(t, "standard", true)
	counter := fakeReviewer(t, describeOut)
	r, code := runCLI(t, "ship", "--dry-run")
	if code != 0 || calls(t, counter) != 0 || !strings.Contains(r.Message, `title "change a.go"`) || !strings.Contains(r.Message, `description "change a.go"`) {
		t.Fatalf("%d calls %d %+v", code, calls(t, counter), r)
	}
}
