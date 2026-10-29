package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBin(t *testing.T, names map[string]string) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for n, body := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil { //nolint:gosec // fake executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestDoctor(t *testing.T) {
	tests := []struct {
		name  string
		tools map[string]string
		code  int
		want  string
		next  string
	}{
		{"all ok", map[string]string{
			"git": "echo git version 2\necho second", "prek": "echo prek 1", "gh": "if [ \"$1\" = auth ]; then echo SECRET-TOKEN; exit 0; fi; echo gh 3",
			"claude": "if [ \"$1\" = auth ]; then echo SECRET-TOKEN >&2; exit 0; fi; echo claude 1",
		}, 0, "gh: present (gh) gh 3; authenticated", ""},
		{"gh unauthenticated", map[string]string{"git": "echo g", "prek": "echo p", "gh": "[ \"$1\" = auth ] && exit 1; echo gh", "codex": "[ \"$1\" = login ] && exit 0; echo c"}, 3, "not authenticated", "authenticate gh"},
		{"reviewer unauthenticated", map[string]string{"git": "echo g", "prek": "echo p", "claude": "[ \"$1\" = auth ] && exit 1; echo c", "codex": "[ \"$1\" = login ] && { echo SECRET-TOKEN >&2; exit 1; }; echo c"}, 3, "codex: present (codex) c; not authenticated", "authenticate"},
		{"codex authenticated", map[string]string{"git": "echo g", "prek": "echo p", "gh": "echo gh", "codex": "[ \"$1\" = login ] && { echo SECRET-TOKEN >&2; exit 0; }; echo c"}, 0, "codex: present (codex) c; authenticated", ""},
		{"no reviewer", map[string]string{"git": "echo g", "prek": "echo p"}, 3, "claude: missing", "reviewer CLI"},
		{"no prek", map[string]string{"git": "echo g", "codex": "echo c"}, 3, "prek: missing", "prek"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeBin(t, tc.tools)
			var out, errb bytes.Buffer
			code := Run([]string{"doctor", "--json"}, strings.NewReader(""), &out, &errb)
			if code != tc.code {
				t.Fatalf("code %d, want %d: %s", code, tc.code, out.String())
			}
			if strings.Contains(out.String()+errb.String(), "SECRET-TOKEN") {
				t.Error("auth output leaked")
			}
			var r Result
			if err := json.Unmarshal(out.Bytes(), &r); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(r.Message, tc.want) || !strings.Contains(r.NextAction, tc.next) {
				t.Errorf("message %q next %q", r.Message, r.NextAction)
			}
			if tc.code != 0 && r.NextAction == "" {
				t.Error("missing next_action")
			}
		})
	}
	var out, errb bytes.Buffer
	if Run([]string{"doctor", "extra"}, strings.NewReader(""), &out, &errb) != ExitUsage {
		t.Error("want usage")
	}
}
