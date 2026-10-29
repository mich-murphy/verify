package state

import (
	"fmt"
	"strings"
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}
func validSHA(s string) bool    { return shaPattern.MatchString(s) }
func validStatus(s string) bool { return s == StatusPass || s == StatusFail }

func validateFinding(f Finding) error {
	if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Message) == "" {
		return invalid("finding requires id and message")
	}
	switch f.Source {
	case SourceGuard, SourceCheck, SourceSetup, SourceReview, SourceVerify:
	default:
		return invalid("finding %s has invalid source %q", f.ID, f.Source)
	}
	if f.Severity != SeverityBlock && f.Severity != SeverityNote {
		return invalid("finding %s has invalid severity", f.ID)
	}
	if f.State != StateOpen && f.State != StateResolved && f.State != StateAccepted {
		return invalid("finding %s has invalid state", f.ID)
	}
	return nil
}

func validateFindings(fs []Finding) error {
	seen := map[string]bool{}
	for _, f := range fs {
		if err := validateFinding(f); err != nil {
			return err
		}
		if seen[f.ID] {
			return invalid("duplicate finding %s", f.ID)
		}
		seen[f.ID] = true
	}
	return nil
}

func validateSteps(steps []Step) error {
	seen := map[string]bool{}
	for _, s := range steps {
		if s.Name == "" || seen[s.Name] || s.DurationMS < 0 {
			return invalid("invalid or duplicate step %q", s.Name)
		}
		seen[s.Name] = true
		if s.Status != StatusPass && s.Status != StatusFail && s.Status != StatusSkipped {
			return invalid("invalid step status %q", s.Status)
		}
		if s.Status == StatusPass && s.ExitCode != 0 {
			return invalid("passing step %s exited nonzero", s.Name)
		}
	}
	return nil
}

func validateCheck(r *CheckRecord) error {
	if !validSHA(r.SHA) || !validSHA(r.MergeBase) {
		return invalid("check requires sha and merge base")
	}
	if r.Lane != "docs" && r.Lane != "standard" && r.Lane != "signoff" {
		return invalid("invalid check lane %q", r.Lane)
	}
	if r.Scope != "affected" && r.Scope != "full" {
		return invalid("invalid check scope %q", r.Scope)
	}
	if r.Scope == "full" && r.ScopeReason == "" {
		return invalid("full check requires scope reason")
	}
	if !validStatus(r.Status) {
		return invalid("invalid check status %q", r.Status)
	}
	if r.Started.IsZero() || r.Finished.IsZero() || r.Finished.Before(r.Started) {
		return invalid("check requires ordered execution timestamps")
	}
	if err := validateSteps(r.Steps); err != nil {
		return err
	}
	if err := validateFindings(r.Findings); err != nil {
		return err
	}
	return checkEvidence(r)
}

func checkEvidence(r *CheckRecord) error {
	blocked := false
	for _, f := range r.Findings {
		if f.Blocking() {
			blocked = true
		}
		if f.Source == SourceReview || f.Source == SourceVerify {
			return invalid("check contains unrelated finding %s", f.ID)
		}
	}
	for _, s := range r.Steps {
		if s.Status == StatusFail {
			blocked = true
		}
	}
	if r.Status == StatusPass && blocked {
		return invalid("passing check contains failed execution or blocking findings")
	}
	if r.Status == StatusFail && !blocked {
		return invalid("failed check has no failure evidence")
	}
	return nil
}

func validateReview(r *ReviewRecord) error {
	if !validSHA(r.SHA) || !validSHA(r.MergeBase) {
		return invalid("review requires sha and merge base")
	}
	if r.Branch == "" || strings.TrimSpace(r.Branch) != r.Branch {
		return invalid("review requires branch identity")
	}
	if r.Reviewer != "claude" && r.Reviewer != "codex" {
		return invalid("invalid reviewer %q", r.Reviewer)
	}
	if !validStatus(r.Status) || r.CallsUsed < 1 || r.CallsUsed > 2 || r.Attempts < r.CallsUsed || r.Attempts > 2 || r.Finished.IsZero() {
		return invalid("incomplete review result")
	}
	if err := validateFindings(r.Findings); err != nil {
		return err
	}
	blocked := false
	for _, f := range r.Findings {
		if f.Source != SourceReview {
			return invalid("review contains unrelated finding %s", f.ID)
		}
		if f.Blocking() {
			blocked = true
		}
	}
	if (r.Status == StatusFail) != blocked {
		return invalid("review status contradicts open findings")
	}
	return nil
}

func validateVerify(r *VerifyRecord) error {
	if !validSHA(r.SHA) || !validSHA(r.MergeBase) || r.Finished.IsZero() {
		return invalid("verify requires sha, merge base and finished time")
	}
	switch r.Verdict {
	case "go", "no-go", "inconclusive", "no-surface":
	default:
		return invalid("invalid verify verdict %q", r.Verdict)
	}
	if err := validateSteps(r.Steps); err != nil {
		return err
	}
	if err := validateFindings(r.Findings); err != nil {
		return err
	}
	if r.Verdict == "no-surface" && (len(r.Steps) > 0 || len(r.Scenarios) > 0 || len(r.Findings) > 0) {
		return invalid("no-surface verify contains execution")
	}
	if r.Verdict == "go" && len(r.Scenarios) == 0 {
		return invalid("go verify requires scenarios")
	}
	return verifyEvidence(r)
}

func verifyEvidence(r *VerifyRecord) error {
	stepByName := map[string]Step{}
	for _, s := range r.Steps {
		stepByName[s.Name] = s
		if r.Verdict == "go" && s.Name == "verify-setup" && s.Status != StatusPass {
			return invalid("go verify has failed setup")
		}
	}
	if err := validateVerifyFindings(r); err != nil {
		return err
	}
	return validateScenarios(r, stepByName)
}

func validateVerifyFindings(r *VerifyRecord) error {
	failedAssertion := false
	inconclusive := false
	for _, f := range r.Findings {
		if f.Source != SourceVerify {
			return invalid("verify contains unrelated finding %s", f.ID)
		}
		if f.Blocking() {
			if f.Rule == "verify:inconclusive" {
				inconclusive = true
			} else {
				failedAssertion = true
			}
		}
		if r.Verdict == "go" && f.Blocking() {
			return invalid("go verify contains blocking finding")
		}
	}
	if r.Verdict == "no-go" && !failedAssertion {
		return invalid("no-go verify requires failed assertion finding")
	}
	if r.Verdict == "inconclusive" && (!inconclusive || failedAssertion) {
		return invalid("inconclusive verify cannot conceal failed assertion")
	}
	return nil
}

func validateScenarios(r *VerifyRecord, stepByName map[string]Step) error {
	seenScenario := map[string]bool{}
	for _, s := range r.Scenarios {
		if s.Name == "" || seenScenario[s.Name] || (s.Status != StatusPass && s.Status != StatusFail && s.Status != StatusSkipped) {
			return invalid("invalid or duplicate scenario %q", s.Name)
		}
		seenScenario[s.Name] = true
		if r.Verdict == "go" {
			if err := validateGoScenario(s, stepByName); err != nil {
				return err
			}
		}
	}
	if r.Verdict == "go" {
		return validateGoSteps(r.Steps, seenScenario)
	}
	return nil
}

func validateGoScenario(s ScenarioResult, byName map[string]Step) error {
	if s.Status != StatusPass {
		return invalid("go verify contains failed scenario")
	}
	step, ok := byName["verify-scenario-"+s.Name]
	// An expected nonzero exit is valid when the scenario assertion passed.
	if !ok || step.ExitCode != s.ExitCode || step.Status == StatusSkipped {
		return invalid("go scenario %q lacks matching executed step", s.Name)
	}
	return nil
}

func validateGoSteps(steps []Step, seen map[string]bool) error {
	for _, step := range steps {
		if step.Name == "verify-setup" {
			continue
		}
		name, ok := strings.CutPrefix(step.Name, "verify-scenario-")
		if !ok || !seen[name] {
			return invalid("go verify has unmatched execution step %s", step.Name)
		}
	}
	return nil
}

func validateBranch(b *Branch, name string) error {
	if name == "" || b.Name != name {
		return invalid("branch identity mismatch: %q vs %q", b.Name, name)
	}
	if b.ReviewCalls < 0 || b.ReviewCalls > 2 || b.ReviewAttempts < b.ReviewCalls || b.LastReviewedSHA != "" && !validSHA(b.LastReviewedSHA) {
		return invalid("invalid branch review budget/history")
	}
	if err := validateFindings(b.Findings); err != nil {
		return err
	}
	for _, f := range b.Findings {
		if f.Source != SourceReview {
			return invalid("branch contains unrelated finding %s", f.ID)
		}
	}
	for _, d := range b.Decisions {
		if err := validateDecision(d); err != nil {
			return err
		}
	}
	return nil
}

func validateDecision(d Decision) error {
	if !validSHA(d.SHA) || strings.TrimSpace(d.Reason) == "" || d.Time.IsZero() {
		return invalid("incomplete decision")
	}
	switch d.Kind {
	case DecideApprove:
		if d.Target != "" {
			return invalid("approve has target")
		}
	case DecideAccept:
		if d.Target == "" {
			return invalid("accept needs target")
		}
	case DecideWaive:
		if d.Target == "" {
			return invalid("waive needs target")
		}
	default:
		return invalid("invalid decision kind %q", d.Kind)
	}
	return nil
}
