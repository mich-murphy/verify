// Package review builds review packets, runs a subscription CLI reviewer in a
// scratch directory and strictly validates its structured answer (SPEC §5).
package review

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// MaxPacket is the largest packet Verify will send.
const MaxPacket = 256 << 10

// ErrTooLarge means the change must be split; Verify never truncates a packet.
var ErrTooLarge = errors.New("review packet exceeds 256 KiB")

// OpenFinding is a previously reported, still open finding shown to a delta review.
type OpenFinding struct {
	ID, Kind, Path string
	Line           int
	Claim          string
}

// PriorFinding records a compact previous disposition without suppressing recurrence.
type PriorFinding struct{ ID, State, Claim, Reason string }

// Input is everything a packet may contain. There is deliberately no field for
// check results or author reasoning.
type Input struct {
	Diff     string
	Messages []string
	Intent   string
	Open     []OpenFinding
	Prior    []PriorFinding
}

// Build renders the packet as deterministic sectioned text.
func Build(in Input) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("=== COMMIT MESSAGES ===\n")
	b.WriteString(strings.Join(in.Messages, "\n---\n") + "\n")
	if t := strings.TrimSpace(in.Intent); t != "" {
		b.WriteString("\n=== INTENT ===\n" + t + "\n")
	}
	if len(in.Open) > 0 {
		b.WriteString("\n=== OPEN FINDINGS ===\n")
		for _, f := range in.Open {
			fmt.Fprintf(&b, "%s [%s] %s:%d\n%s\n", f.ID, f.Kind, f.Path, f.Line, f.Claim)
		}
	}
	if len(in.Prior) > 0 {
		b.WriteString("\n=== PRIOR FINDING DISPOSITIONS ===\n")
		for _, f := range in.Prior {
			fmt.Fprintf(&b, "%s [%s] %s (reason: %s)\n", f.ID, f.State, f.Claim, f.Reason)
		}
	}
	b.WriteString("\n=== DIFF ===\n" + in.Diff)
	if b.Len() > MaxPacket {
		return nil, fmt.Errorf("%w (%d bytes)", ErrTooLarge, b.Len())
	}
	return b.Bytes(), nil
}
