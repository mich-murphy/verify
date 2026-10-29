package state

import (
	"fmt"
	"sync"
	"testing"
)

func TestBattleConcurrentBranchUpdatesPreserveAllFindings(t *testing.T) {
	st := Open(t.TempDir())
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := range workers {
		wg.Go(func() {
			errs <- st.UpdateBranch("feature", func(b *Branch) error {
				// Findings are append-only identity history, just as decisions are. Use
				// notes here so this exercises locking independently of budget policy.
				b.Findings = append(b.Findings, Finding{ID: fmt.Sprintf("F%d", worker+1), Source: SourceReview, Severity: SeverityNote, State: StateOpen, Message: "claim"})
				return nil
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	br, err := st.LoadBranch("feature")
	if err != nil {
		t.Fatal(err)
	}
	if len(br.Findings) != workers {
		t.Fatalf("lost update: got %d findings, want %d", len(br.Findings), workers)
	}
	if br.NextFindingID() != "F9" {
		t.Fatalf("identity history lost: next ID %s", br.NextFindingID())
	}
}
