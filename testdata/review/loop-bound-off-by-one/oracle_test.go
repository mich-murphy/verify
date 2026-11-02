package fixture

import "testing"

func TestChecksumSumsEveryByte(t *testing.T) {
	if got := Checksum([]byte{1, 2, 3}); got != 6 {
		t.Fatalf("got %d, want 6", got)
	}
}
