package state

import "time"

// Schema identifiers. Every schema is version 1 and there are no migrations.
const (
	SchemaBranch = "gate.branch/1"
	SchemaCheck  = "gate.check/1"
	SchemaReview = "gate.review/1"
	SchemaVerify = "gate.verify/1"
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
)

// Repro is the concrete input and expectation behind a finding.
type Repro struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

// Finding is the shared shape for everything Gate reports.
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

// Step is one executed command.
type Step struct {
	Name       string `json:"name"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Log        string `json:"log,omitempty"`
	Status     string `json:"status"`
}

// Decision kinds.
const (
	DecideApprove = "approve"
	DecideAccept  = "accept"
	DecideWaive   = "waive"
)

// Decision is a recorded human decision.
type Decision struct {
	Kind   string    `json:"kind"`
	Target string    `json:"target,omitempty"`
	Reason string    `json:"reason,omitempty"`
	SHA    string    `json:"sha"`
	Time   time.Time `json:"time"`
}

// Branch is gate.branch/1: per-branch review state.
type Branch struct {
	Schema          string     `json:"schema"`
	Name            string     `json:"name"`
	ReviewCalls     int        `json:"review_calls"`
	ReviewAttempts  int        `json:"review_attempts"`
	Findings        []Finding  `json:"findings"`
	LastReviewedSHA string     `json:"last_reviewed_sha,omitempty"`
	Parked          bool       `json:"parked"`
	Decisions       []Decision `json:"decisions"`
}

// CheckRecord is gate.check/1.
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
	Started       time.Time `json:"started"`
	Finished      time.Time `json:"finished"`
}

// ReviewRecord is gate.review/1.
type ReviewRecord struct {
	Schema    string    `json:"schema"`
	SHA       string    `json:"sha"`
	MergeBase string    `json:"merge_base"`
	Branch    string    `json:"branch"`
	Reviewer  string    `json:"reviewer"`
	Delta     bool      `json:"delta"`
	Status    string    `json:"status"` // pass | fail
	Findings  []Finding `json:"findings"`
	Resolved  []string  `json:"resolved,omitempty"`
	Finished  time.Time `json:"finished"`
	CallsUsed int       `json:"calls_used"`
	Attempts  int       `json:"attempts"`
}

// ScenarioResult is one verify scenario outcome.
type ScenarioResult struct {
	Name     string `json:"name"`
	ExitCode int    `json:"exit_code"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

// VerifyRecord is gate.verify/1.
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
