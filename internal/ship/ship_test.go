package ship

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/state"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

const fakeGH = `#!/bin/sh
echo "$*" >> "$GH_LOG"
case "$1 $2" in
"pr view")
  if [ -f "$GH_PR" ]; then echo '{"number":7,"url":"https://example.test/pr/7","state":"OPEN"}'; exit 0; fi
  echo "no pull requests found" >&2; exit 1 ;;
"pr create") echo "https://example.test/pr/9"; : > "$GH_PR" ;;
"api --hostname") if [ -f "$GH_FAIL_API" ]; then echo "boom" >&2; exit 1; fi ;;
esac
exit 0
`

type env struct {
	repo    *gitx.Repo
	dir     string
	origin  string
	sha     string
	ghLog   string
	prFile  string
	failAPI string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", root)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@e")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@e")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o700); err != nil { // #nosec G306 -- fake executable
		t.Fatal(err)
	}
	e := &env{dir: filepath.Join(root, "work"), origin: filepath.Join(root, "origin.git")}
	e.ghLog, e.prFile, e.failAPI = filepath.Join(root, "gh.log"), filepath.Join(root, "pr"), filepath.Join(root, "failapi")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_LOG", e.ghLog)
	t.Setenv("GH_PR", e.prFile)
	t.Setenv("GH_FAIL_API", e.failAPI)
	git(t, root, "init", "-q", "--bare", "-b", "main", e.origin)
	git(t, root, "init", "-q", "-b", "feature", e.dir)
	if err := os.WriteFile(filepath.Join(e.dir, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, e.dir, "add", ".")
	git(t, e.dir, "commit", "-q", "-m", "first")
	git(t, e.dir, "remote", "add", "origin", "git@github.com:example/project.git")
	ssh := filepath.Join(bin, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\ncase \"$*\" in *git-upload-pack*) exec git-upload-pack \"$VERIFY_BARE\";; *git-receive-pack*) exec git-receive-pack \"$VERIFY_BARE\";; esac\nexit 1\n"), 0o700); err != nil { // #nosec G306 -- fake executable in test temp directory
		t.Fatal(err)
	}
	t.Setenv("VERIFY_BARE", e.origin)
	e.sha = git(t, e.dir, "rev-parse", "HEAD")
	var err error
	if e.repo, err = gitx.Open(t.Context(), e.dir); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) opts(dry bool) Options {
	return Options{Repo: e.repo, Remote: "origin", Branch: "feature", SHA: e.sha, Base: "main", Title: "first", Body: "body", Lane: "standard", DryRun: dry}
}

func (e *env) log(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(e.ghLog)
	return string(b)
}

func TestDryRunRunsNothing(t *testing.T) {
	e := newEnv(t)
	rep, err := Run(t.Context(), e.opts(true))
	if err != nil || len(rep.Steps) != 3 {
		t.Fatalf("%v %+v", err, rep)
	}
	for _, s := range rep.Steps {
		if s.Status != Planned {
			t.Errorf("%+v", s)
		}
	}
	if e.log(t) != "" || git(t, e.origin, "branch", "--list", "feature") != "" {
		t.Fatal("dry run did something")
	}
}

func TestShipCreatesPR(t *testing.T) {
	e := newEnv(t)
	rep, err := Run(t.Context(), e.opts(false))
	if err != nil {
		t.Fatalf("%v %+v", err, rep)
	}
	if got := git(t, e.origin, "rev-parse", "refs/heads/feature"); got != e.sha {
		t.Fatalf("remote at %s", got)
	}
	log := e.log(t)
	for _, want := range []string{
		"pr view feature --repo github.com/example/project --json number,url,state", "pr create --repo github.com/example/project --head feature --base main --title first --body-file",
		"api --hostname github.com -X POST repos/example/project/statuses/" + e.sha + " -f state=success -f context=verify/verified -f description=",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if rep.PRURL != "https://example.test/pr/9" {
		t.Errorf("url %q", rep.PRURL)
	}
}

func TestShipEditsExistingPRAndUsesLease(t *testing.T) {
	e := newEnv(t)
	if _, err := Run(t.Context(), e.opts(false)); err != nil {
		t.Fatal(err)
	}
	// second commit: push must fast-forward from the leased value
	if err := os.WriteFile(filepath.Join(e.dir, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, e.dir, "add", ".")
	git(t, e.dir, "commit", "-q", "-m", "second")
	e.sha = git(t, e.dir, "rev-parse", "HEAD")
	if err := os.WriteFile(e.ghLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(t.Context(), e.opts(false))
	if err != nil {
		t.Fatalf("%v %+v", err, rep)
	}
	if got := git(t, e.origin, "rev-parse", "refs/heads/feature"); got != e.sha {
		t.Fatalf("remote at %s", got)
	}
	if log := e.log(t); !strings.Contains(log, "pr edit 7 --repo github.com/example/project --body-file") || strings.Contains(log, "pr create") {
		t.Fatalf("log:\n%s", log)
	}
	if rep.PRURL != "https://example.test/pr/7" {
		t.Errorf("url %q", rep.PRURL)
	}
}

func TestPushLeaseRejectsMovedRemote(t *testing.T) {
	e := newEnv(t)
	other, _ := filepath.EvalSymlinks(t.TempDir())
	git(t, other, "clone", "-q", e.origin, "c")
	c := filepath.Join(other, "c")
	git(t, c, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(c, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, c, "add", ".")
	git(t, c, "commit", "-q", "-m", "x")
	git(t, c, "push", "-q", "origin", "feature")
	// A stale expected value must be rejected.
	if err := e.repo.PushLease(t.Context(), "origin", "feature", e.sha, ""); err == nil {
		t.Fatal("lease should reject")
	}
}

func TestFailureAfterPushReportsRemaining(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(e.failAPI, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(t.Context(), e.opts(false))
	if err == nil || len(rep.Steps) != 3 || rep.Steps[0].Status != Done || rep.Steps[1].Status != Done || rep.Steps[2].Status != Failed {
		t.Fatalf("%v %+v", err, rep)
	}
	e2 := newEnv(t)
	e2.opts(false)
	o := e2.opts(false)
	o.Remote = "nowhere"
	rep, err = Run(t.Context(), o)
	if err == nil || rep.Steps[0].Status != Failed || rep.Steps[1].Status != Pending || rep.Steps[2].Status != Pending {
		t.Fatalf("%v %+v", err, rep)
	}
}

func TestSelectedRemoteControlsAllGitHubCalls(t *testing.T) {
	e := newEnv(t)
	git(t, e.dir, "remote", "add", "fork", "git@github.com:fork-owner/fork-repo.git")
	target, err := remoteTarget(t.Context(), Options{Repo: e.repo, Remote: "fork"})
	if err != nil || target.repo != "fork-owner/fork-repo" {
		t.Fatalf("target %q %v", target, err)
	}
	o := e.opts(true)
	o.Remote = "fork"
	rep, err := Run(t.Context(), o)
	if err != nil || len(rep.Steps) != 3 || !strings.Contains(rep.Steps[1].Detail, "fork-owner/fork-repo") || !strings.Contains(rep.Steps[2].Detail, "fork-owner/fork-repo") {
		t.Fatalf("fork plan %+v %v", rep, err)
	}
	git(t, e.dir, "remote", "set-url", "--push", "fork", "https://github.com/other/target.git")
	target, err = remoteTarget(t.Context(), Options{Repo: e.repo, Remote: "fork"})
	if err != nil || target.repo != "other/target" {
		t.Fatalf("push URL must determine target: %q %v", target, err)
	}
	git(t, e.dir, "remote", "set-url", "--add", "--push", "fork", "https://github.com/other/second.git")
	if _, err = remoteTarget(t.Context(), Options{Repo: e.repo, Remote: "fork"}); err == nil {
		t.Fatal("ambiguous push targets must fail")
	}
	git(t, e.dir, "remote", "set-url", "--delete", "--push", "fork", "https://github.com/other/second.git")
	git(t, e.dir, "remote", "set-url", "--push", "fork", "https://secret:token@example.org/other/repo.git")
	if _, err = remoteTarget(t.Context(), Options{Repo: e.repo, Remote: "fork"}); err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") {
		t.Fatalf("must reject invalid remote without leaking credentials: %v", err)
	}
}

func TestInvalidArgs(t *testing.T) {
	e := newEnv(t)
	if _, err := e.repo.RemoteBranchSHA(t.Context(), "-x", "b"); err == nil {
		t.Fatal("remote")
	}
	if err := e.repo.PushLease(t.Context(), "origin", "a:b", e.sha, ""); err == nil {
		t.Fatal("branch")
	}
	if err := e.repo.PushLease(t.Context(), "origin", "feature", "HEAD", ""); err == nil {
		t.Fatal("sha")
	}
	if s, err := e.repo.FirstSubject(t.Context(), e.sha, e.sha); err != nil || s != "" {
		t.Fatalf("%q %v", s, err)
	}
}

func TestBody(t *testing.T) {
	sha := strings.Repeat("a", 40)
	b := Body(BodyInput{
		SHA: sha, Lane: "signoff",
		Check: &state.CheckRecord{
			Status: "pass", Steps: []state.Step{{Name: "prek", Status: "pass"}},
			Findings: []state.Finding{{ID: "guard:x", Severity: "note", State: "open", Message: "a\nnote"}},
		},
		Review: &state.ReviewRecord{Reviewer: "codex", Status: "pass"},
		Verify: &state.VerifyRecord{Verdict: "go"},
		Branch: &state.Branch{
			Findings:  []state.Finding{{ID: "F1", Severity: "block", State: "accepted", Message: "bug"}, {ID: "F2", Severity: "block", State: "open", Message: "open one"}},
			Decisions: []state.Decision{{Kind: "approve", SHA: sha, Reason: "ok"}, {Kind: "approve", SHA: "other", Reason: "no"}},
		},
	})
	for _, want := range []string{"signoff", "- executed prek: pass", "review (codex): pass", "verify: go", "`F1` [accepted] bug", "### Notes", "a note", "approve : ok"} {
		if !strings.Contains(b, want) {
			t.Errorf("body lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "F2") || strings.Contains(b, "no\n") {
		t.Errorf("unexpected content:\n%s", b)
	}
	if !strings.Contains(Body(BodyInput{SHA: sha, Lane: "docs"}), "docs") {
		t.Error("minimal body")
	}
}
