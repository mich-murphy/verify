package fixture

import "testing"

func TestCanAdmitExactFit(t *testing.T) {
	if !CanAdmit(10, 7, 3) {
		t.Fatal("a request that exactly fits must be admitted")
	}
	if CanAdmit(10, 7, 4) || CanAdmit(5, 9, 0) {
		t.Fatal("over-limit request admitted")
	}
}
