package fixture

import (
	"slices"
	"testing"
)

func TestWindowClampsToLength(t *testing.T) {
	got := Window([]int{1, 2, 3}, 1, 5)
	if !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("got %v, want [2 3]", got)
	}
}
