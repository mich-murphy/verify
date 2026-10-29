package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/state"
)

const (
	cleanOut = `{"findings":[],"resolved":[]}`
	blockOut = `{"findings":[{"kind":"correctness","path":"a.go","line":3,"claim":"wrong","repro":{"input":"1","expected":"2"}}],"resolved":[]}`
	fixedOut = `{"findings":[],"resolved":["F1"]}`
)

// fakeReviewer installs a fake claude on PATH. Call n prints outs[n] (the last
// repeats) wrapped like claude's JSON result; a "!" prefix prints it raw.
// It returns the file counting invocations.
func fakeReviewer(t *testing.T, outs ...string) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	counter := filepath.Join(dir, "n")
	write(t, dir, "n", "0")
	var cases strings.Builder
	for i, o := range outs {
		body := `{"structured_output":` + o + `}`
		if raw, ok := strings.CutPrefix(o, "!"); ok {
			body = raw
		}
		cases.WriteString(strconv.Itoa(i) + `) /bin/cat <<'EOF_VERIFY'` + "\n" + body + "\nEOF_VERIFY\n;;\n")
	}
	last := strconv.Itoa(len(outs) - 1)
	script := "#!/bin/sh\n/bin/cat > '" + filepath.Join(dir, "packet") + "'\nread n < '" + counter + "'\necho $((n+1)) > '" + counter + "'\n" +
		"[ \"$n\" -gt " + last + " ] && n=" + last + "\ncase $n in\n" + cases.String() + "esac\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700); err != nil { //nolint:gosec // fake executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return counter
}

func calls(t *testing.T, counter string) int {
	t.Helper()
	b, err := os.ReadFile(counter) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func loadBranch(t *testing.T, repo string) *state.Branch {
	t.Helper()
	b, err := state.Open(filepath.Join(repo, ".git")).LoadBranch("feature")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// passCheck records a passing check for HEAD directly; the real check's setup
// audit findings are outside what review tests exercise.
func passCheck(t *testing.T, repo string) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", "-C", repo, "rev-parse", "HEAD").Output() // #nosec G204 -- fixed test command
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))
	base := strings.TrimSpace(gitOut(t, repo, "rev-parse", "main"))
	now := time.Now().UTC()
	rec := &state.CheckRecord{SHA: sha, MergeBase: base, Lane: "standard", Scope: "affected", Status: state.StatusPass, Started: now, Finished: now}
	if err := state.Open(filepath.Join(repo, ".git")).SaveCheck(rec); err != nil {
		t.Fatal(err)
	}
}

func checked(t *testing.T) string {
	t.Helper()
	repo := setup(t, "fake")
	commit(t, repo, "a.go", "package a\n\nvar X = 1\n")
	passCheck(t, repo)
	return repo
}

func TestReviewNeedsCheck(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "a.go", "package a\n\nvar X = 1\n")
	r, code := runCLI(t, "review")
	if code != ExitBlocked || r.NextAction != "verify check" {
		t.Fatalf("%d %+v", code, r)
	}
}

func TestReviewDocsLane(t *testing.T) {
	repo := setup(t, "fake")
	commit(t, repo, "README.md", "changed\n")
	r, code := runCLI(t, "review")
	if code != 0 || !strings.Contains(r.Message, "review not required") {
		t.Fatalf("%d %+v", code, r)
	}
}

func TestReviewPassAndBlockAndDelta(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, blockOut, fixedOut)
	r, code := runCLI(t, "review", "--reviewer", "claude")
	if code != ExitFindings || len(r.Findings) != 1 || r.Findings[0].ID != "F1" || r.Findings[0].Severity != "block" {
		t.Fatalf("%d %+v", code, r)
	}
	b := loadBranch(t, repo)
	if b.ReviewCalls != 1 || b.LastReviewedSHA != r.SHA {
		t.Fatalf("%+v", b)
	}
	commit(t, repo, "a.go", "package a\n\nvar X = 2\n")
	passCheck(t, repo)
	r, code = runCLI(t, "review", "--reviewer", "claude")
	if code != 0 || r.Status != "pass" || len(r.Findings) != 0 {
		t.Fatalf("%d %+v", code, r)
	}
	b = loadBranch(t, repo)
	if b.ReviewCalls != 2 || b.Findings[0].State != state.StateResolved || calls(t, counter) != 2 {
		t.Fatalf("%+v", b)
	}
	// Budget (2) is now exhausted: the next review parks the branch.
	commit(t, repo, "a.go", "package a\n\nvar X = 3\n")
	passCheck(t, repo)
	r, code = runCLI(t, "review", "--reviewer", "claude")
	if code != ExitBlocked || !loadBranch(t, repo).Parked || calls(t, counter) != 2 {
		t.Fatalf("%d %+v", code, r)
	}
	r, code = runCLI(t, "review", "--reviewer", "claude")
	if code != ExitBlocked || r.NextAction != "verify decide accept the open findings, then verify decide approve" {
		t.Fatalf("%d %+v", code, r)
	}
}

func TestCleanSecondReviewUsesDelta(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, cleanOut, cleanOut)
	if r, code := runCLI(t, "review", "--reviewer", "claude"); code != 0 {
		t.Fatalf("initial: %d %+v", code, r)
	}
	commit(t, repo, "a.go", "package a\n\nvar X = 2\n")
	passCheck(t, repo)
	if r, code := runCLI(t, "review", "--reviewer", "claude"); code != 0 {
		t.Fatalf("second: %d %+v", code, r)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(counter), "packet"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "+var X = 2") || strings.Contains(string(data), "+var X = 1") {
		t.Fatalf("second packet not a delta: %s", data)
	}
	rec, err := state.Open(filepath.Join(repo, ".git")).LoadReview(strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD")))
	if err != nil || !rec.Delta || rec.Status != state.StatusPass {
		t.Fatalf("second review: %+v %v", rec, err)
	}
}

func TestAcceptedFindingCanGenuinelyRecur(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, blockOut, blockOut)
	if _, code := runCLI(t, "review", "--reviewer", "claude"); code != ExitFindings {
		t.Fatalf("initial review: %d", code)
	}
	if _, code := runCLI(t, "decide", "accept", "F1", "--reason", "intentional old behavior"); code != ExitOK {
		t.Fatalf("accept: %d", code)
	}
	commit(t, repo, "a.go", "package a\n\nvar X = 2\n")
	passCheck(t, repo)
	r, code := runCLI(t, "review", "--reviewer", "claude")
	if code != ExitFindings || len(r.Findings) != 1 || r.Findings[0].ID != "F2" {
		t.Fatalf("recurrence should be new ID: %d %+v", code, r)
	}
	b := loadBranch(t, repo)
	if b.Findings[0].State != state.StateAccepted || b.Findings[1].State != state.StateOpen {
		t.Fatalf("wrong dispositions: %+v", b.Findings)
	}
	packet, err := os.ReadFile(filepath.Join(filepath.Dir(counter), "packet"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packet), "intentional old behavior") {
		t.Fatalf("missing human disposition: %s", packet)
	}
}

func TestAcceptRecordedFindingAfterBranchHistoryLost(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, blockOut)
	r, code := runCLI(t, "review", "--reviewer", "claude")
	if code != ExitFindings || r.Findings[0].ID != "F1" {
		t.Fatalf("initial: %d %+v", code, r)
	}
	st := state.Open(filepath.Join(repo, ".git"))
	if err := os.Remove(st.BranchPath("feature")); err != nil {
		t.Fatal(err)
	}
	s, _ := runCLI(t, "status")
	if !strings.Contains(s.Message, "blocking: F1") {
		t.Fatalf("immutable finding disappeared: %+v", s)
	}
	if _, code = runCLI(t, "decide", "accept", "F1", "--reason", "human accepts risk"); code != ExitOK {
		t.Fatalf("accept recorded-only finding: %d", code)
	}
	b := loadBranch(t, repo)
	if len(b.Findings) != 1 || b.Findings[0].State != state.StateAccepted || len(b.Decisions) != 1 {
		t.Fatalf("disposition not persisted: %+v", b)
	}
	s, _ = runCLI(t, "status")
	if strings.Contains(s.Message, "blocking: F1") || !strings.Contains(s.Message, "eligible: yes") || s.Findings[0].State != state.StateAccepted {
		t.Fatalf("accept not effective: %+v", s)
	}
	if calls(t, counter) != 1 {
		t.Fatalf("unexpected reviewer call: %d", calls(t, counter))
	}
}

func TestConcurrentReviewsDoNotOverspend(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, cleanOut)
	start := make(chan struct{})
	results := make(chan int, 3)
	for range 3 {
		go func() {
			<-start
			var out, errb strings.Builder
			results <- Run([]string{"review", "--reviewer", "claude", "--json"}, strings.NewReader(""), &out, &errb)
		}()
	}
	close(start)
	for range 3 {
		if code := <-results; code != ExitOK {
			t.Fatalf("same-SHA review must reuse: %d", code)
		}
	}
	b := loadBranch(t, repo)
	if b.ReviewCalls != 1 || b.ReviewAttempts != 1 || b.Parked || calls(t, counter) != 1 {
		t.Fatalf("same-SHA concurrent reviews: branch %+v calls %d", b, calls(t, counter))
	}
	commit(t, repo, "a.go", "package a\n\nvar X = 2\n")
	passCheck(t, repo)
	if r, code := runCLI(t, "review", "--reviewer", "claude"); code != ExitOK || r.Status != state.StatusPass {
		t.Fatalf("repair delta: %d %+v", code, r)
	}
	b = loadBranch(t, repo)
	if b.ReviewCalls != 2 || b.ReviewAttempts != 2 || b.Parked || calls(t, counter) != 2 {
		t.Fatalf("repair must consume second review: %+v calls %d", b, calls(t, counter))
	}
	commit(t, repo, "a.go", "package a\n\nvar X = 3\n")
	passCheck(t, repo)
	if r, code := runCLI(t, "review", "--reviewer", "claude"); code != ExitBlocked || !loadBranch(t, repo).Parked || calls(t, counter) != 2 {
		t.Fatalf("attempted third review should park: %d %+v", code, r)
	}
}

func TestFailedReviewSameSHAReusesFailure(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, blockOut, cleanOut)
	if r, code := runCLI(t, "review", "--reviewer", "claude"); code != ExitFindings || r.Status != state.StatusFail {
		t.Fatalf("initial: %d %+v", code, r)
	}
	r, code := runCLI(t, "review", "--reviewer", "claude")
	if code != ExitFindings || r.Status != state.StatusFail || len(r.Findings) != 1 || calls(t, counter) != 1 || loadBranch(t, repo).ReviewCalls != 1 {
		t.Fatalf("failed record must not silently pass: %d %+v calls %d", code, r, calls(t, counter))
	}
	st := state.Open(filepath.Join(repo, ".git"))
	path, _ := st.RunDir(r.SHA)
	before, err := os.ReadFile(filepath.Join(path, "review.json")) // #nosec G304 -- test-owned persisted review record
	if err != nil {
		t.Fatal(err)
	}
	if _, code := runCLI(t, "decide", "accept", "F1", "--reason", "human accepts this risk"); code != ExitOK {
		t.Fatalf("accept: %d", code)
	}
	r, code = runCLI(t, "review", "--reviewer", "claude")
	if code != ExitOK || r.Status != state.StatusPass || !strings.Contains(r.Message, "stored raw review failed") {
		t.Fatalf("accepted effective review should pass: %d %+v", code, r)
	}
	after, err := os.ReadFile(filepath.Join(path, "review.json")) // #nosec G304 -- test-owned persisted review record
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || calls(t, counter) != 1 || loadBranch(t, repo).ReviewAttempts != 1 {
		t.Fatalf("raw review mutated or subprocess retried")
	}
	stored, err := st.LoadReview(r.SHA)
	if err != nil || stored.Status != state.StatusFail || stored.Attempts != 1 {
		t.Fatalf("stored raw evidence changed: %+v %v", stored, err)
	}
}

func TestReviewInvalidRetriedOnce(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, `{"findings":[{"bogus":1}],"resolved":[]}`, cleanOut)
	_, code := runCLI(t, "review", "--reviewer", "claude")
	branch := loadBranch(t, repo)
	if code != 0 || calls(t, counter) != 2 || branch.ReviewCalls != 1 || branch.ReviewAttempts != 2 {
		t.Fatalf("code %d calls %d branch %+v", code, calls(t, counter), branch)
	}
	sha := strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD"))
	rec, err := state.Open(filepath.Join(repo, ".git")).LoadReview(sha)
	if err != nil || rec.CallsUsed != 1 || rec.Attempts != 2 {
		t.Fatalf("invalid retry logical/attempt evidence: %+v %v", rec, err)
	}
	s, _ := runCLI(t, "status")
	if !strings.Contains(s.Message, "review subprocess attempts: 2 total") || !strings.Contains(s.Message, "review attempts for SHA: 2") {
		t.Fatalf("attempts missing from status: %+v", s)
	}
}

func TestReviewInvalidTwiceFails(t *testing.T) {
	repo := checked(t)
	counter := fakeReviewer(t, `{"nope":1}`)
	_, code := runCLI(t, "review", "--reviewer", "claude")
	b := loadBranch(t, repo)
	if code != ExitBlocked || calls(t, counter) != 2 || b.ReviewCalls != 1 || b.ReviewAttempts != 2 {
		t.Fatalf("code %d calls %d branch %+v", code, calls(t, counter), b)
	}
}

func TestReviewRateLimitConsumesNothing(t *testing.T) {
	repo := checked(t)
	fakeReviewer(t, `!{"is_error":true,"result":"usage limit reached"}`)
	r, code := runCLI(t, "review", "--reviewer", "claude")
	b := loadBranch(t, repo)
	if code != ExitBlocked || b.ReviewCalls != 0 || b.ReviewAttempts != 1 || !strings.Contains(r.NextAction, "doctor") {
		t.Fatalf("%d %+v branch %+v", code, r, b)
	}
}

func TestReviewMissingCLI(t *testing.T) {
	repo := checked(t)
	_, code := runCLI(t, "review", "--reviewer", "claude")
	b := loadBranch(t, repo)
	if code != ExitBlocked || b.ReviewCalls != 0 || b.ReviewAttempts != 1 {
		t.Fatalf("code %d branch %+v", code, b)
	}
}

func TestReviewUsageErrors(t *testing.T) {
	checked(t)
	if _, code := runCLI(t, "review", "--reviewer", "gpt"); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
	if _, code := runCLI(t, "review", "--intent", "/nonexistent"); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
}

func TestReviewIntentAndTooLarge(t *testing.T) {
	repo := checked(t)
	fakeReviewer(t, cleanOut)
	intent := filepath.Join(t.TempDir(), "intent.txt")
	write(t, filepath.Dir(intent), "intent.txt", "why\n")
	if _, code := runCLI(t, "review", "--reviewer", "claude", "--intent", intent); code != 0 {
		t.Fatalf("code %d", code)
	}
	// A 300 KiB change exceeds the packet bound.
	repo2 := setup(t, "fake")
	commit(t, repo2, "a.go", "package a\n\n// "+strings.Repeat("x", 300<<10)+"\n")
	passCheck(t, repo2)
	fakeReviewer(t, cleanOut)
	r, code := runCLI(t, "review", "--reviewer", "claude")
	if code != ExitBlocked || !strings.Contains(r.NextAction, "split") {
		t.Fatalf("%d %+v", code, r)
	}
	_ = repo
}
