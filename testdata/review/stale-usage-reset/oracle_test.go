package fixture

import "testing"

func TestWindowResetsCredits(t *testing.T) {
	u := &Usage{}
	u.Update(1, 4)
	u.Update(2, 3)
	if u.Credits != 3 {
		t.Fatalf("new window: got %d, want 3", u.Credits)
	}
}
