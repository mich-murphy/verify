package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentHook(t *testing.T) {
	tests := []struct {
		name, in string
		args     []string
		code     int
		stderr   string
	}{
		{"block", `{"toolName":"bash","input":{"command":"git commit -n"}}`, nil, 2, "blocked"},
		{"block json", `{"tool_name":"Write","tool_input":{"file_path":"gate.yaml"}}`, []string{"--json"}, 2, ""},
		{"allow", `{"toolName":"bash","input":{"command":"ls"}}`, nil, 0, ""},
		{"allow json", `{"toolName":"bash","input":{"command":"ls"}}`, []string{"--json"}, 0, ""},
		{"malformed", `{x`, nil, 0, "warning"},
		{"usage", ``, []string{"extra"}, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := Run(append([]string{"agent-hook"}, tc.args...), strings.NewReader(tc.in), &out, &errb)
			if code != tc.code || !strings.Contains(errb.String(), tc.stderr) {
				t.Errorf("code %d stderr %q", code, errb.String())
			}
			if len(tc.args) > 0 && tc.args[0] == "--json" {
				var r Result
				if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Schema != ResultSchema || errb.Len() != 0 || (code != 0 && r.NextAction == "") {
					t.Errorf("result %+v, stderr %q, error %v", r, errb.String(), err)
				}
			}
		})
	}
}
