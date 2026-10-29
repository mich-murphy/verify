package fixture

import "testing"

func TestOptOut(t *testing.T) {
	if got := Redirect(false, "/home"); got != "" {
		t.Fatalf("disabled redirect: %q", got)
	}
	if got := Redirect(true, "/home"); got != "/home" {
		t.Fatalf("enabled redirect: %q", got)
	}
}
