package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestCheckRoundTripAndFailClosed(t *testing.T) {
	s := Open(t.TempDir())
	now := time.Now().UTC()
	rec := &CheckRecord{SHA: sha, MergeBase: sha, Lane: "standard", Scope: "affected", Status: "pass", Started: now, Finished: now, Steps: []Step{{Name: "a", Status: StatusPass}}}
	if err := s.SaveCheck(rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadCheck(sha)
	if err != nil || got.Schema != SchemaCheck || got.Steps[0].Name != "a" {
		t.Fatalf("%+v %v", got, err)
	}
	d, _ := s.RunDir(sha)
	p := filepath.Join(d, "check.json")
	for name, content := range map[string]string{
		"garbage":  "{not json",
		"unknown":  `{"schema":"gate.check/1","sha":"` + sha + `","zzz":1}`,
		"schema":   `{"schema":"gate.check/2","sha":"` + sha + `"}`,
		"trailing": `{"schema":"gate.check/1","sha":"` + sha + `"} {}`,
		"wrongsha": `{"schema":"gate.check/1","sha":"abc"}`,
	} {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.LoadCheck(sha); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: want ErrCorrupt, got %v", name, err)
		}
	}
	_ = os.Remove(p)
	if _, err := s.LoadCheck(sha); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReviewVerifyRoundTrip(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.SaveReview(&ReviewRecord{SHA: sha, MergeBase: sha, Branch: "feature", Reviewer: "codex", Status: "pass", CallsUsed: 1, Attempts: 1, Finished: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVerify(&VerifyRecord{SHA: sha, MergeBase: sha, Verdict: "go", Scenarios: []ScenarioResult{{Name: "help", Status: StatusPass}}, Steps: []Step{{Name: "verify-scenario-help", Status: StatusPass}}, Finished: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if r, err := s.LoadReview(sha); err != nil || r.Schema != SchemaReview {
		t.Fatal(r, err)
	}
	if v, err := s.LoadVerify(sha); err != nil || v.Verdict != "go" {
		t.Fatal(v, err)
	}
}

func TestReviewAttemptValidation(t *testing.T) {
	s := Open(t.TempDir())
	good := ReviewRecord{SHA: sha, MergeBase: sha, Branch: "feature", Reviewer: "codex", Status: StatusPass, CallsUsed: 1, Attempts: 1, Finished: time.Now().UTC()}
	for _, n := range []int{0, 3} {
		r := good
		r.Attempts = n
		if err := s.SaveReview(&r); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("invalid attempt count %d: %v", n, err)
		}
	}
	if err := s.UpdateBranch("feature", func(b *Branch) error { b.ReviewCalls = 1; return nil }); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("branch with fewer attempts than reviews: %v", err)
	}
}

func TestBranch(t *testing.T) {
	s := Open(t.TempDir())
	b, err := s.LoadBranch("feat/x")
	if err != nil || b.Name != "feat/x" {
		t.Fatal(b, err)
	}
	if b.NextFindingID() != "F1" {
		t.Fatal(b.NextFindingID())
	}
	err = s.UpdateBranch("feat/x", func(b *Branch) error {
		b.ReviewCalls++
		b.ReviewAttempts++
		b.Findings = append(b.Findings, Finding{ID: "F1", Source: SourceReview, Severity: SeverityNote, State: StateOpen, Message: "one"}, Finding{ID: "F7", Source: SourceReview, Severity: SeverityNote, State: StateOpen, Message: "seven"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.LoadBranch("feat/x")
	if err != nil || b.ReviewCalls != 1 || b.NextFindingID() != "F8" || b.Schema != SchemaBranch {
		t.Fatalf("%+v %v", b, err)
	}
	if strings.Contains(filepath.Base(s.BranchPath("a/b")), "/") {
		t.Fatal("slash not escaped")
	}
}

func TestSemanticValidation(t *testing.T) {
	s := Open(t.TempDir())
	now := time.Now().UTC()
	good := CheckRecord{SHA: sha, MergeBase: sha, Lane: "docs", Scope: "affected", Status: StatusPass, Started: now, Finished: now}
	for _, tc := range []struct {
		name   string
		mutate func(*CheckRecord)
	}{
		{"missing status", func(r *CheckRecord) { r.Status = "" }},
		{"missing base", func(r *CheckRecord) { r.MergeBase = "" }},
		{"unknown lane", func(r *CheckRecord) { r.Lane = "" }},
		{"missing scope", func(r *CheckRecord) { r.Scope = "" }},
		{"full without reason", func(r *CheckRecord) { r.Scope = "full" }},
		{"passing failed step", func(r *CheckRecord) { r.Steps = []Step{{Name: "prek", Status: StatusFail, ExitCode: 1}} }},
		{"passing blocker", func(r *CheckRecord) {
			r.Findings = []Finding{{ID: "setup:x", Source: SourceSetup, Severity: SeverityBlock, State: StateOpen, Message: "missing"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := good
			tc.mutate(&r)
			if err := s.SaveCheck(&r); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("wanted corrupt: %v", err)
			}
		})
	}
	if err := s.SaveCheck(&good); err != nil {
		t.Fatal(err)
	}
	d, _ := s.RunDir(sha)
	p := filepath.Join(d, "check.json")
	raw, err := os.ReadFile(p) // #nosec G304 -- test-owned state file
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), `"status": "pass"`, `"status": ""`, 1)
	if err := os.WriteFile(p, []byte(bad), 0o600); err != nil { // #nosec G703 -- test-owned state file
		t.Fatal(err)
	}
	if _, err := s.LoadCheck(sha); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("read should reject incomplete docs evidence: %v", err)
	}
}

func TestVerifyCoherence(t *testing.T) {
	s := Open(t.TempDir())
	good := VerifyRecord{SHA: sha, MergeBase: sha, Verdict: "go", Finished: time.Now().UTC(), Scenarios: []ScenarioResult{{Name: "help", Status: StatusPass, ExitCode: 2}}, Steps: []Step{{Name: "verify-scenario-help", Status: StatusFail, ExitCode: 2}}}
	if err := s.SaveVerify(&good); err != nil {
		t.Fatalf("expected nonzero scenario is valid: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*VerifyRecord)
	}{
		{"missing execution", func(v *VerifyRecord) { v.Steps = nil }},
		{"wrong exit", func(v *VerifyRecord) { v.Steps[0].ExitCode = 1 }},
		{"duplicate scenario", func(v *VerifyRecord) { v.Scenarios = append(v.Scenarios, v.Scenarios[0]) }},
		{"failed setup", func(v *VerifyRecord) {
			v.Steps = append(v.Steps, Step{Name: "verify-setup", Status: StatusFail, ExitCode: 1})
		}},
		{"extra step", func(v *VerifyRecord) { v.Steps = append(v.Steps, Step{Name: "verify-unrelated", Status: StatusPass}) }},
		{"inconclusive cannot conceal assertion", func(v *VerifyRecord) {
			v.Verdict = "inconclusive"
			v.Findings = []Finding{{ID: "verify:inconclusive", Source: SourceVerify, Rule: "verify:inconclusive", State: StateOpen, Severity: SeverityBlock, Message: "timeout"}, {ID: "verify:scenario:help", Source: SourceVerify, Rule: "verify:scenario:help", State: StateOpen, Severity: SeverityBlock, Message: "assertion failed"}}
		}},
		{"inconclusive requires exact rule", func(v *VerifyRecord) {
			v.Verdict = "inconclusive"
			v.Findings = []Finding{{ID: "verify:inconclusive:fake", Source: SourceVerify, Rule: "verify:inconclusive:fake", State: StateOpen, Severity: SeverityBlock, Message: "invalid"}}
		}},
		{"no-go requires assertion", func(v *VerifyRecord) {
			v.Verdict = "no-go"
			v.Findings = []Finding{{ID: "verify:inconclusive", Source: SourceVerify, Rule: "verify:inconclusive", State: StateOpen, Severity: SeverityBlock, Message: "timeout"}}
		}},
		{"no-go without finding", func(v *VerifyRecord) { v.Verdict = "no-go" }},
		{"inconclusive without finding", func(v *VerifyRecord) { v.Verdict = "inconclusive" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := good
			v.Steps = append([]Step(nil), good.Steps...)
			v.Scenarios = append([]ScenarioResult(nil), good.Scenarios...)
			tc.mutate(&v)
			if err := s.SaveVerify(&v); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("wanted corrupt: %v", err)
			}
		})
	}
}

func TestBranchIdentityAndInjectivity(t *testing.T) {
	s := Open(t.TempDir())
	if s.BranchPath("feat/x") == s.BranchPath("feat%2Fx") {
		t.Fatal("branch filenames collide")
	}
	if err := s.UpdateBranch("feat/x", func(*Branch) error { return nil }); err != nil {
		t.Fatal(err)
	}
	p := s.BranchPath("feat/x")
	raw, err := os.ReadFile(p) // #nosec G304 -- test-owned branch file
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(raw), `"feat/x"`, `"feat%2Fx"`, 1)), 0o600); err != nil { // #nosec G703 -- test-owned branch file
		t.Fatal(err)
	}
	if _, err := s.LoadBranch("feat/x"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("branch identity mismatch must fail closed: %v", err)
	}
}

func TestLogPathsDoNotAlias(t *testing.T) {
	s := Open(t.TempDir())
	paths := map[string]string{}
	for _, name := range []string{"x/y", "x_y", "x%2Fy", "x:y", "x.y"} {
		path, err := s.LogPath(sha, name)
		if err != nil {
			t.Fatal(err)
		}
		if prev := paths[path]; prev != "" {
			t.Fatalf("log names %q and %q collide at %s", prev, name, path)
		}
		paths[path] = name
	}
	if _, err := s.LogPath(sha, ""); err == nil {
		t.Fatal("empty step must be rejected")
	}
}

func TestInvalidSHA(t *testing.T) {
	s := Open(t.TempDir())
	if _, err := s.RunDir("../x"); err == nil {
		t.Fatal("want error")
	}
	if p, _ := s.LogPath(sha, "../../etc"); !strings.HasPrefix(p, s.Dir) {
		t.Fatal(p)
	}
}

func TestWriteAtomicNoTempLeft(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "x", "f")
	if err := WriteAtomic(p, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("b")); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatal(ents)
	}
	if b, _ := os.ReadFile(p); string(b) != "b" { //nolint:gosec // test path
		t.Fatal(string(b))
	}
}

func TestLockExclusive(t *testing.T) {
	s := Open(t.TempDir())
	u, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	u()
	u2, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	u2()
}
