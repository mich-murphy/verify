package fixture

import "testing"

func TestAverage(t *testing.T) {
	if Average(nil) != 0 {
		t.Fatal("empty slice must average to 0")
	}
	if got := Average([]int{5}); got != 5 {
		t.Fatalf("Average([5]) = %d, want 5", got)
	}
	if got := Average([]int{2, 4, 6}); got != 4 {
		t.Fatalf("Average([2 4 6]) = %d, want 4", got)
	}
}
