package fixture

import "testing"

func TestMarkerBudget(t *testing.T) {
	got := MarkRecent([]string{"a", "b", "c"})
	if got[0] != "a" || got[1] != "b [cache]" || got[2] != "c [cache]" {
		t.Fatalf("marker budget: %v", got)
	}
}
