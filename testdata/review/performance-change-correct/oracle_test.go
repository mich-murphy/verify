package fixture

import (
	"slices"
	"testing"
)

func TestUniqueKeepsFirstOccurrence(t *testing.T) {
	got := Unique([]string{"b", "a", "b", "c", "a"})
	if !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Fatalf("got %v", got)
	}
	if len(Unique(nil)) != 0 {
		t.Fatal("nil input must stay empty")
	}
}
