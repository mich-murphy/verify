package verify

import (
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/runner"
)

func TestBattleContainsLongerThanEntireOutput(t *testing.T) {
	want := "too long"
	bad, unknown := assertions(config.Scenario{StdoutContains: &want}, runner.Result{Stdout: runner.Evidence{Output: "a", Truncated: true, Bytes: 2}})
	if bad == "" || unknown {
		t.Fatalf("2-byte output cannot contain %q: bad=%q unknown=%v", want, bad, unknown)
	}
}

// Equality may only pass if every compatible completion passes, and may only
// fail if every completion fails. Two different suffixes witness uncertainty.
func TestBattleEqualityPrefixProperty(t *testing.T) {
	values := []string{"", "a", "b", "aa", "ab", "ba", "bb"}
	for _, whole := range values {
		for keep := 0; keep <= len(whole); keep++ {
			for _, want := range values {
				bad, unknown := assertions(config.Scenario{StdoutEquals: &want}, runner.Result{Stdout: runner.Evidence{Output: whole[:keep], Truncated: keep < len(whole), Bytes: int64(len(whole))}})
				mismatch := len(want) != len(whole) || !strings.HasPrefix(want, whole[:keep])
				switch {
				case mismatch && (bad == "" || unknown):
					t.Fatalf("provable mismatch: whole=%q keep=%d want=%q: %q %v", whole, keep, want, bad, unknown)
				case !mismatch && keep < len(whole) && (bad != "" || !unknown):
					t.Fatalf("undecidable suffix: whole=%q keep=%d want=%q: %q %v", whole, keep, want, bad, unknown)
				case !mismatch && keep == len(whole) && (bad != "" || unknown):
					t.Fatalf("complete match: whole=%q want=%q: %q %v", whole, want, bad, unknown)
				}
			}
		}
	}
}
