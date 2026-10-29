package fixture

import "testing"

func TestValidName(t *testing.T) {
	if ValidName("") || !ValidName("a") || !ValidName(" ") {
		t.Fatal("unchanged validation: empty names rejected, whitespace-only names allowed")
	}
	if Label("a") != "entry: a" {
		t.Fatal("display label not updated")
	}
}
