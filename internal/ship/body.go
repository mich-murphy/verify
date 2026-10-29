package ship

import (
	"fmt"
	"strings"

	"github.com/mich-murphy/verify/internal/eligible"
	"github.com/mich-murphy/verify/internal/state"
)

// BodyInput is the evidence summarised in the PR body.
type BodyInput struct {
	SHA         string
	Lane        string
	LaneReasons []string
	Check       *state.CheckRecord
	Review      *state.ReviewRecord
	Verify      *state.VerifyRecord
	Branch      *state.Branch
}

// Body renders the PR description.
func Body(in BodyInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Verify\n\nCommit `%s`, **%s** lane.\n", in.SHA, in.Lane)
	if len(in.LaneReasons) > 0 {
		fmt.Fprintf(&b, "Lane reasons: %s.\n", strings.Join(in.LaneReasons, "; "))
	}
	b.WriteString("\n### Local evidence (not CI or merge approval)\n\n")
	if in.Check != nil {
		fmt.Fprintf(&b, "- check scope: %s", in.Check.Scope)
		if in.Check.ScopeReason != "" {
			fmt.Fprintf(&b, " (%s)", oneLine(in.Check.ScopeReason))
		}
		b.WriteString("\n")
		for _, s := range in.Check.Steps {
			fmt.Fprintf(&b, "- executed %s: %s (exit %d)\n", s.Name, s.Status, s.ExitCode)
		}
		fmt.Fprintf(&b, "- check: %s\n", in.Check.Status)
	}
	if in.Review != nil {
		fmt.Fprintf(&b, "- review (%s): %s\n", in.Review.Reviewer, in.Review.Status)
	}
	if in.Verify != nil {
		fmt.Fprintf(&b, "- verify: %s\n", in.Verify.Verdict)
		for _, sc := range in.Verify.Scenarios {
			fmt.Fprintf(&b, "- scenario %s: %s (exit %d)%s\n", sc.Name, sc.Status, sc.ExitCode, oneLine(sc.Detail))
		}
	}
	b.WriteString("\nReviewer findings below are claims, not executed reproductions. Verify does not assert CI results, merge approval or rollback readiness.\n")
	writeFindings(&b, in)
	writeDecisions(&b, in)
	return b.String()
}

func allFindings(in BodyInput) []state.Finding {
	return eligible.EffectiveFindings(in.Check, in.Review, in.Verify, in.Branch, in.SHA)
}

func writeFindings(b *strings.Builder, in BodyInput) {
	var notes, handled []string
	for _, f := range allFindings(in) {
		line := fmt.Sprintf("- `%s` [%s] %s", f.ID, f.State, oneLine(f.Message))
		switch {
		case f.Severity == state.SeverityNote:
			notes = append(notes, line)
		case f.State != state.StateOpen:
			handled = append(handled, line)
		}
	}
	if len(handled) > 0 {
		b.WriteString("\n### Blocking findings resolved or accepted\n\n" + strings.Join(handled, "\n") + "\n")
	}
	if len(notes) > 0 {
		b.WriteString("\n### Notes\n\n" + strings.Join(notes, "\n") + "\n")
	}
}

func writeDecisions(b *strings.Builder, in BodyInput) {
	if in.Branch == nil {
		return
	}
	var lines []string
	for _, d := range in.Branch.Decisions {
		if d.SHA != in.SHA {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s %s: %s", d.Kind, d.Target, oneLine(d.Reason)))
	}
	if len(lines) > 0 {
		b.WriteString("\n### Decisions\n\n" + strings.Join(lines, "\n") + "\n")
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
