package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVerifyCLI(t *testing.T) {
	repo := setup(t, "none")
	commit(t, repo, "other.txt", "x\n")
	r, code := runCLI(t, "verify")
	if code != 0 || r.Status != "no-surface" {
		t.Fatalf("%d %+v", code, r)
	}
	var out, errb bytes.Buffer
	if code = Run([]string{"verify", "extra"}, strings.NewReader(""), &out, &errb); code != ExitUsage {
		t.Errorf("code %d", code)
	}
	write(t, repo, "verify.yaml", baseYAML+"verify:\n  surfaces: [\"cmd/**\"]\n  scenarios:\n    - {name: s, run: [/bin/sh, -c, \"echo hi\"], exit: 1}\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "cfg")
	// config comes from the merge-base (no surfaces there), so still no-surface.
	if r, code = runCLI(t, "verify"); code != 0 {
		t.Fatalf("%d %+v", code, r)
	}
}
