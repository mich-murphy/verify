package ship

import (
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/state"
)

func TestBattleBodyShowsEffectiveGuardDisposition(t *testing.T) {
	sha := strings.Repeat("a", 40)
	body := Body(BodyInput{
		SHA: sha, Lane: "docs",
		Check:  &state.CheckRecord{Status: state.StatusFail, Findings: []state.Finding{{ID: "guard:suppressions", Source: state.SourceGuard, Severity: state.SeverityBlock, State: state.StateOpen, Message: "suppression in a.go"}}},
		Branch: &state.Branch{Decisions: []state.Decision{{Kind: state.DecideAccept, Target: "guard:suppressions", SHA: sha, Reason: "intentional"}}},
	})
	if !strings.Contains(body, "check: fail") {
		t.Fatal("raw execution result lost")
	}
	if !strings.Contains(body, "`guard:suppressions` [accepted] suppression in a.go") {
		t.Fatalf("effective accepted finding and claim omitted from PR body:\n%s", body)
	}
}

func TestBattleGitHubTargetTransportMatrix(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"git@github.com:owner/repo.git", true},
		{"ssh://git@github.com/owner/repo.git", true},
		{"https://github.com/owner/repo.git", true},
		{"https://github.com.evil.test/owner/repo.git", false},
		{"ssh://other@github.com/owner/repo.git", false},
		{"ssh://github.com/owner/repo.git", false},
		{"https://github.com:443/owner/repo.git", false},
		{"https://github.com/owner/repo.git?destination=other", false},
		{"https://github.com/owner/repo.git#other", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			_, err := githubPath(tc.raw)
			if (err == nil) != tc.ok {
				t.Fatalf("githubPath(%q): %v", tc.raw, err)
			}
		})
	}
}
