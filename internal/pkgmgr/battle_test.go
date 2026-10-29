package pkgmgr

import (
	"os/exec"
	"testing"
)

func TestBattleQuoteArgRoundTrip(t *testing.T) {
	for _, arg := range []string{"", "web app", "web'app", "web; echo bad", "web\napp", "--flag=value"} {
		cmd := exec.CommandContext(t.Context(), "sh", "-c", "printf '%s' "+QuoteArg(arg)) // #nosec G204 -- fixture arguments quoted by function under test
		got, err := cmd.Output()
		if err != nil || string(got) != arg {
			t.Fatalf("round trip %q: %q %v", arg, got, err)
		}
	}
}
