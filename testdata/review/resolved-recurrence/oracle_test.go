package fixture

import "testing"

func TestValidName(t *testing.T) {
	if ValidName("") || !ValidName("a") {
		t.Fatal("empty names must be rejected")
	}
}
