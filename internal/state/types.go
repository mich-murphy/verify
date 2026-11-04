package state

import "time"

// Schema identifiers. Every schema is version 1 and there are no migrations.
const (
	SchemaBranch = "verify.branch/1"
	SchemaCheck  = "verify.check/1"
	SchemaReview = "verify.review/1"
	SchemaVerify = "verify.verify/1"
	SchemaTree   = "verify.tree/1"
)

// Finding sources, severities and states.
const (
	SourceGuard  = "guard"
	SourceCheck  = "check"
	SourceSetup  = "setup"
	SourceReview = "review"
	SourceVerify = "verify"

	SeverityBlock = "block"
	SeverityNote  = "note"

	StateOpen     = "open"
	StateResolved = "resolved"
	StateAccepted = "accepted"
	StateWaived   = "waived"
)

// Repro is the concrete input and expectation behind a finding.
type Repro struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

// Finding is the shared shape for everything Verify reports.
type Finding struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
	Repro    *Repro `json:"repro,omitempty"`
	State    string `json:"state"`
	// Hook, Log and Excerpt locate a check failure: the failing hook, the
	// per-attempt log path, and at most 20 lines (2 KiB) of its output.
	Hook    string `json:"hook,omitempty"`
	Log     string `json:"log,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

// Blocking reports whether the finding is open and blocks.
func (f Finding) Blocking() bool {
	return f.Severity == SeverityBlock && f.State == StateOpen
}

// Step statuses.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusSkipped = "skipped"
)

// Hook statuses, as parsed from prek output.
const (
	HookPass     = "pass"
	HookFail     = "fail"
	HookSkipped  = "skipped"
	HookModified = "modified"
)

// HookResult is one prek hook inside a prek stage step.
type HookResult struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	ExitCode   int    `json:"exit_code,omitempty"`
}

// Step is one executed command.
type Step struct {
	Name       string       `json:"name"`
	ExitCode   int          `json:"exit_code"`
	DurationMS int64        `json:"duration_ms"`
	Log        string       `json:"log,omitempty"`
	Status     string       `json:"status"`
	Hooks      []HookResult `json:"hooks,omitempty"`
}

// Decision kinds.
const (
	DecideApprove = "approve"
	DecideAccept  = "accept"
	DecideWaive   = "waive"
)

// DecisionCoverage is the evidence behind an approval: the range it covers
// and the check record it relies on.
type DecisionCoverage struct {
	From  string `json:"from"` // last reviewed sha, or the merge-base when nothing was reviewed
	To    string `json:"to"`   // the approved sha
	Files int    `json:"files"`
	Lines int    `json:"lines"`
	Check string `json:"check"` // sha of the check record relied on
}

// Decision is a recorded human decision.
type Decision struct {
	Kind     string            `json:"kind"`
	Target   string            `json:"target,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	SHA      string            `json:"sha"`
	Time     time.Time         `json:"time"`
	Coverage *DecisionCoverage `json:"coverage,omitempty"`
}

// Sources of a PR description.
const (
	DescribedByClaude  = "claude"
	DescribedByCodex   = "codex"
	DescribedByCommits = "commits"
)

// Description is the PR title and body composed for one SHA. It is not a review
// and spends nothing on the review budget.
type Description struct {
	SHA    string `json:"sha"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"` // why the reviewer CLI did not write it; empty when it did
}

// Branch is verify.branch/1: per-branch review state.
type Branch struct {
	Schema          string       `json:"schema"`
	Name            string       `json:"name"`
	ReviewAttempts  int          `json:"review_attempts"`
	Findings        []Finding    `json:"findings"`
	LastReviewedSHA string       `json:"last_reviewed_sha,omitempty"`
	Decisions       []Decision   `json:"decisions"`
	Description     *Description `json:"description,omitempty"`
}

// CheckRecord is verify.check/1.
type CheckRecord struct {
	Schema        string    `json:"schema"`
	SHA           string    `json:"sha"`
	MergeBase     string    `json:"merge_base"`
	Lane          string    `json:"lane"`
	LaneReasons   []string  `json:"lane_reasons,omitempty"`
	Scope         string    `json:"scope"`
	ScopeReason   string    `json:"scope_reason,omitempty"`
	Status        string    `json:"status"` // pass | fail
	Steps         []Step    `json:"steps"`
	Findings      []Finding `json:"findings"`
	ConfigFromCan bool      `json:"config_from_candidate,omitempty"`
	Tree          string    `json:"tree"`
	PrekVersion   string    `json:"prek_version"`
	ReusedFrom    string    `json:"reused_from,omitempty"`
	Attempt       int       `json:"attempt"`
	Started       time.Time `json:"started"`
	Finished      time.Time `json:"finished"`
}

// TreeIndex is verify.tree/1: trees/<tree>.json names the latest sha whose check on that tree passed.
type TreeIndex struct {
	Schema string `json:"schema"`
	SHA    string `json:"sha"`
}

// ReviewRecord is verify.review/1.
type ReviewRecord struct {
	Schema    string `json:"schema"`
	SHA       string `json:"sha"`
	MergeBase string `json:"merge_base"`
	Branch    string `json:"branch"`
	Reviewer  string `json:"reviewer"`
	// FallbackFrom names the family that was unavailable when auto fell back.
	FallbackFrom string    `json:"fallback_from,omitempty"`
	Delta        bool      `json:"delta"`
	Status       string    `json:"status"` // pass | fail
	Findings     []Finding `json:"findings"`
	Resolved     []string  `json:"resolved,omitempty"`
	Finished     time.Time `json:"finished"`
	CallsUsed    int       `json:"calls_used"`
	Attempts     int       `json:"attempts"`
}

// ScenarioResult is one verify scenario outcome.
type ScenarioResult struct {
	Name     string `json:"name"`
	ExitCode int    `json:"exit_code"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

// VerifyRecord is verify.verify/1.
type VerifyRecord struct {
	Schema    string           `json:"schema"`
	SHA       string           `json:"sha"`
	MergeBase string           `json:"merge_base"`
	Verdict   string           `json:"verdict"` // go | no-go | inconclusive | no-surface
	Steps     []Step           `json:"steps"`
	Scenarios []ScenarioResult `json:"scenarios,omitempty"`
	Findings  []Finding        `json:"findings"`
	Finished  time.Time        `json:"finished"`
}
