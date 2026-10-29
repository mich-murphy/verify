package fixture

import "testing"

func TestClamp(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-1, 0}, {2, 2}, {12, 10}} {
		if got := Clamp(tc.in, 0, 10); got != tc.want {
			t.Fatalf("Clamp(%d)=%d want %d", tc.in, got, tc.want)
		}
	}
}
