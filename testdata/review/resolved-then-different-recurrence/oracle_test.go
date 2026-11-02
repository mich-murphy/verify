package fixture

import "testing"

func TestSlug(t *testing.T) {
	if got := Slug("Hello Big World"); got != "hello-big-world" {
		t.Fatalf("Slug = %q, want hello-big-world", got)
	}
}
