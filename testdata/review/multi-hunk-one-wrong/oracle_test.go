package fixture

import "testing"

func TestHelpers(t *testing.T) {
	if Mean([]int{2, 4, 6}) != 4 || Mean(nil) != 0 {
		t.Fatal("Mean changed")
	}
	if Clamp(9, 0, 5) != 5 || Clamp(-1, 0, 5) != 0 || Clamp(3, 0, 5) != 3 {
		t.Fatal("Clamp changed")
	}
	if got := Percent(1, 4); got != 25 {
		t.Fatalf("Percent(1, 4) = %d, want 25", got)
	}
}
