package check

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mich-murphy/verify/internal/state"
)

func TestParsePrekOutput(t *testing.T) {
	lintOut := []string{"a.go:3: bad thing", "second line"}
	cases := []struct {
		file  string
		hooks []state.HookResult
		lines map[string][]string
	}{
		{"default.txt", []state.HookResult{
			{ID: "ok hook", Status: "pass"},
			{ID: "lint", Status: "fail", ExitCode: 2},
			{ID: "end-of-file-fixer", Status: "modified"},
		}, map[string][]string{"lint": lintOut}},
		{"verbose.txt", []state.HookResult{
			{ID: "ok-hook", Status: "pass", DurationMs: 320},
			{ID: "lint", Status: "fail", DurationMs: 0, ExitCode: 2},
			{ID: "end-of-file-fixer", Status: "modified", DurationMs: 10},
		}, map[string][]string{"lint": lintOut}},
		{"verbose-skip-chatty.txt", []state.HookResult{
			{ID: "ok-hook", Status: "pass", DurationMs: 320},
			{ID: "lint", Status: "fail", ExitCode: 2},
			{ID: "end-of-file-fixer", Status: "modified", DurationMs: 10},
			{ID: "python only hook with a rather long display name that goes past the width", Status: "skipped"},
			{ID: "chatty", Status: "pass"},
		}, map[string][]string{"lint": lintOut, "chatty": {"chatter-from-passing-hook"}}},
		{"prek-error.txt", nil, map[string][]string{}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "prek", c.file)) // #nosec G304 -- fixed test fixture names
			if err != nil {
				t.Fatal(err)
			}
			hooks, lines := parsePrekOutput(raw)
			if !reflect.DeepEqual(hooks, c.hooks) {
				t.Errorf("hooks\n got %+v\nwant %+v", hooks, c.hooks)
			}
			if !reflect.DeepEqual(lines, c.lines) {
				t.Errorf("lines\n got %q\nwant %q", lines, c.lines)
			}
		})
	}
}
