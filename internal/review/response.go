package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/mich-murphy/verify/internal/state"
)

// ErrInvalidOutput means the reviewer produced output that fails validation.
var ErrInvalidOutput = errors.New("invalid reviewer output")

const (
	maxClaim    = 2000
	maxFindings = 100
)

// Response is a validated review answer. Findings have no ID yet.
type Response struct {
	Findings []state.Finding
	Resolved []string
}

type rawResponse struct {
	Findings *[]rawFinding `json:"findings"`
	Resolved *[]string     `json:"resolved"`
}

type rawFinding struct {
	Kind  *string         `json:"kind"`
	Path  *string         `json:"path"`
	Line  *int            `json:"line"`
	Claim *string         `json:"claim"`
	Repro json.RawMessage `json:"repro"`
}

type rawRepro struct {
	Input    *string `json:"input"`
	Expected *string `json:"expected"`
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOutput, fmt.Sprintf(format, a...))
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalid("%v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return invalid("trailing data after JSON value")
	}
	return nil
}

// Parse validates raw strictly and never repairs it. A correctness finding
// with a repro blocks; everything else is a note.
func Parse(raw []byte, openIDs, diffPaths []string) (*Response, error) {
	var r rawResponse
	if err := decodeStrict(raw, &r); err != nil {
		return nil, err
	}
	if r.Findings == nil || r.Resolved == nil {
		return nil, invalid("findings and resolved are required")
	}
	if len(*r.Findings) > maxFindings {
		return nil, invalid("more than %d findings", maxFindings)
	}
	out := &Response{Findings: []state.Finding{}, Resolved: []string{}}
	for i, rf := range *r.Findings {
		f, err := rf.validate(diffPaths)
		if err != nil {
			return nil, invalid("finding %d: %v", i, err)
		}
		out.Findings = append(out.Findings, f)
	}
	for _, id := range *r.Resolved {
		if !slices.Contains(openIDs, id) || slices.Contains(out.Resolved, id) {
			return nil, invalid("resolved %q is not an open finding", id)
		}
		out.Resolved = append(out.Resolved, id)
	}
	return out, nil
}

func (rf rawFinding) validate(diffPaths []string) (state.Finding, error) {
	if rf.Kind == nil || rf.Path == nil || rf.Line == nil || rf.Claim == nil || rf.Repro == nil {
		return state.Finding{}, errors.New("kind, path, line, claim and repro are required")
	}
	switch {
	case !slices.Contains(Kinds, *rf.Kind):
		return state.Finding{}, fmt.Errorf("unknown kind %q", *rf.Kind)
	case !slices.Contains(diffPaths, *rf.Path):
		return state.Finding{}, fmt.Errorf("path %q is not a changed path", *rf.Path)
	case *rf.Line < 0:
		return state.Finding{}, errors.New("negative line")
	case *rf.Claim == "" || len(*rf.Claim) > maxClaim:
		return state.Finding{}, fmt.Errorf("claim must be 1..%d bytes", maxClaim)
	}
	f := state.Finding{
		Source: state.SourceReview, Rule: *rf.Kind, Severity: state.SeverityNote,
		Path: *rf.Path, Line: *rf.Line, Message: *rf.Claim, State: state.StateOpen,
	}
	if string(bytes.TrimSpace(rf.Repro)) == "null" {
		return f, nil
	}
	var rr rawRepro
	if err := decodeStrict(rf.Repro, &rr); err != nil {
		return state.Finding{}, err
	}
	if rr.Input == nil || rr.Expected == nil || *rr.Input == "" || *rr.Expected == "" ||
		len(*rr.Input) > maxClaim || len(*rr.Expected) > maxClaim {
		return state.Finding{}, errors.New("repro needs non-empty input and expected")
	}
	f.Repro = &state.Repro{Input: *rr.Input, Expected: *rr.Expected}
	if f.Rule == "correctness" {
		f.Severity = state.SeverityBlock
	}
	return f, nil
}
