package ship

import (
	"strings"
	"testing"
)

func TestFallbackDescription(t *testing.T) {
	const note = "Description generated from commit messages (reviewer CLI unavailable)"
	for _, tc := range []struct {
		name         string
		branch       string
		subjects     []string
		title, descr string
	}{
		{"one commit uses its subject", "fix-login", []string{"Fix the login redirect"}, "Fix the login redirect", "Fix the login redirect\n\n" + note},
		{"several commits prefix the humanised branch", "fix-login-redirect", []string{"Add test", "Fix the redirect"}, "fix login redirect: Fix the redirect", "Add test\nFix the redirect\n\n" + note},
		{"no commits fall back to the branch", "fix-login", nil, "fix login", note},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title, descr := FallbackDescription(tc.branch, tc.subjects)
			if title != tc.title || descr != tc.descr {
				t.Fatalf("got %q / %q, want %q / %q", title, descr, tc.title, tc.descr)
			}
		})
	}
}

func TestBodyPutsTheDescriptionBeforeTheEvidence(t *testing.T) {
	sha := strings.Repeat("a", 40)
	b := Body(BodyInput{SHA: sha, Lane: "standard", Description: "It changes the thing.\nIt matters."})
	if !strings.HasPrefix(b, "It changes the thing.\nIt matters.\n\n## Verify\n\nCommit `"+sha+"`") {
		t.Fatalf("body does not start with the description then the evidence section:\n%s", b)
	}
}
