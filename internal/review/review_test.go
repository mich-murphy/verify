package review

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSchemaAllRequired(t *testing.T) {
	var s map[string]any
	if err := json.Unmarshal([]byte(Schema), &s); err != nil {
		t.Fatal(err)
	}
	var walk func(n any)
	walk = func(n any) {
		m, ok := n.(map[string]any)
		if !ok {
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			req, _ := m["required"].([]any)
			if len(req) != len(props) || m["additionalProperties"] != false {
				t.Errorf("object not closed/required: %v", m)
			}
			for _, v := range props {
				walk(v)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items)
		}
		if any, ok := m["anyOf"].([]any); ok {
			for _, v := range any {
				walk(v)
			}
		}
	}
	walk(s)
}

func TestParse(t *testing.T) {
	paths := []string{"a.go"}
	ids := []string{"F1"}
	f := func(kind, path, repro string) string {
		return `{"findings":[{"kind":"` + kind + `","path":"` + path + `","line":3,"claim":"bad","repro":` + repro + `}],"resolved":[]}`
	}
	rep := `{"input":"x","expected":"y"}`
	tests := []struct {
		name, raw string
		ok        bool
		sev       string
	}{
		{"block", f("correctness", "a.go", rep), true, "block"},
		{"no repro note", f("correctness", "a.go", "null"), true, "note"},
		{"contract note", f("contract", "a.go", rep), true, "note"},
		{"bad kind", f("style", "a.go", "null"), false, ""},
		{"bad path", f("data", "z.go", "null"), false, ""},
		{"empty repro field", f("data", "a.go", `{"input":"","expected":"y"}`), false, ""},
		{"repro extra", f("data", "a.go", `{"input":"x","expected":"y","z":1}`), false, ""},
		{"unknown field", `{"findings":[],"resolved":[],"extra":1}`, false, ""},
		{"missing resolved", `{"findings":[]}`, false, ""},
		{"bad resolved", `{"findings":[],"resolved":["F9"]}`, false, ""},
		{"good resolved", `{"findings":[],"resolved":["F1"]}`, true, ""},
		{"dup resolved", `{"findings":[],"resolved":["F1","F1"]}`, false, ""},
		{"empty claim", `{"findings":[{"kind":"data","path":"a.go","line":0,"claim":"","repro":null}],"resolved":[]}`, false, ""},
		{"long claim", `{"findings":[{"kind":"data","path":"a.go","line":0,"claim":"` + strings.Repeat("x", 3000) + `","repro":null}],"resolved":[]}`, false, ""},
		{"trailing", `{"findings":[],"resolved":[]} x`, false, ""},
		{"not json", `sorry`, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse([]byte(tc.raw), ids, paths)
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
			if err != nil && !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("want ErrInvalidOutput: %v", err)
			}
			if tc.sev != "" && r.Findings[0].Severity != tc.sev {
				t.Fatalf("severity %s", r.Findings[0].Severity)
			}
		})
	}
}

func TestPacket(t *testing.T) {
	in := Input{Diff: "diff", Messages: []string{"m1", "m2"}, Intent: "why", Open: []OpenFinding{{"F1", "data", "a.go", 4, "claim"}}}
	a, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Build(in)
	if string(a) != string(b) {
		t.Fatal("nondeterministic")
	}
	for _, w := range []string{"m1", "why", "F1 [data] a.go:4", "=== DIFF ===\ndiff"} {
		if !strings.Contains(string(a), w) {
			t.Errorf("missing %q", w)
		}
	}
	_, err = Build(Input{Diff: strings.Repeat("x", MaxPacket)})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestReviewerSubprocessOutputBound(t *testing.T) {
	s, err := newScratch()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	out, _, err := s.proc(t.Context(), 10*time.Second, "sh", []string{"-c", "head -c 1048577 /dev/zero"}, nil, s.env())
	if err == nil || len(out) != maxReviewerOutput {
		t.Fatalf("subprocess output should be bounded: len=%d err=%v", len(out), err)
	}
}

func TestReviewerTimeoutSettlesDescendants(t *testing.T) {
	s, err := newScratch()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	pidFile := filepath.Join(s.root, "child.pid")
	_, _, err = s.proc(t.Context(), 400*time.Millisecond, "sh", []string{"-c", "sleep 30 & echo $! > \"$1\"; wait", "sh", pidFile}, nil, s.env())
	if !errors.Is(err, errReviewTimeout) {
		t.Fatalf("expected bounded group timeout: %v", err)
	}
	raw, err := os.ReadFile(pidFile) // #nosec G304 -- test-owned pid path under private scratch directory
	if err != nil {
		t.Fatalf("child did not start: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output() // #nosec G204 -- test-only fixed ps command and parsed pid.
	if err == nil && !strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
		t.Fatalf("reviewer descendant %d remained live: %s", pid, out)
	}
}

func TestSelect(t *testing.T) {
	for _, tc := range []struct{ choice, author, want string }{
		{"auto", "", "codex"},
		{"", "claude", "codex"},
		{"auto", "codex", "claude"},
		{"claude", "claude", "claude"},
		{"codex", "codex", "codex"},
	} {
		r, err := Select(tc.choice, tc.author, "m", "n")
		if err != nil || r.Name() != tc.want {
			t.Errorf("%+v -> %v %v", tc, r, err)
		}
	}
	if _, err := Select("gpt", "", "", ""); err == nil {
		t.Fatal("want error")
	}
}

// fake writes an executable shell script into a fresh dir and returns its path
// and a log path that receives argv, env and stdin.
func fake(t *testing.T, body string) (bin, log string) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	log = filepath.Join(dir, "log")
	bin = filepath.Join(dir, "fake")
	script := "#!/bin/sh\n{ echo ARGV; for a in \"$@\"; do echo \"[$a]\"; done; echo ENV; env; echo CWD; pwd; echo STDIN; cat; } > '" + log + "'\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // fake executable
		t.Fatal(err)
	}
	return bin, log
}

func readLog(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const okOut = `{"findings":[],"resolved":[]}`

func TestClaudeRun(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("OPENAI_API_KEY", "sk-secret2")
	t.Setenv("HOME", "/h")
	bin, log := fake(t, `cat <<'X'
{"is_error":false,"structured_output":`+okOut+`,"result":"ignored","usage":{"input_tokens":5,"output_tokens":7}}
X
`)
	out, u, err := (&Claude{Model: "opus", Bin: bin}).Run(t.Context(), []byte("PACKET"))
	if err != nil || string(out) != okOut || u.InputTokens != 5 || u.OutputTokens != 7 {
		t.Fatalf("%s %+v %v", out, u, err)
	}
	l := readLog(t, log)
	for _, w := range []string{
		"[-p]", "[--output-format]", "[json]", "[--json-schema]", "[opus]", "[--tools]", "[]", "[--setting-sources]",
		"[--strict-mcp-config]", "[--disable-slash-commands]", "[--no-session-persistence]", "[--system-prompt]", "[" + SystemPrompt + "]", "HOME=/h", "PACKET",
	} {
		if !strings.Contains(l, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(l, "sk-secret") || strings.Contains(l, "API_KEY") {
		t.Fatal("API key leaked")
	}
	if !strings.Contains(l, "TMPDIR=") || !strings.Contains(l, "verify-review-") {
		t.Fatal("TMPDIR not private")
	}
	cwd := strings.TrimSpace(strings.Split(strings.Split(l, "CWD\n")[1], "\n")[0])
	if _, err := os.Stat(cwd); err == nil {
		t.Fatal("scratch dir not removed")
	}
}

func TestClaudeResultFallbackAndErrors(t *testing.T) {
	bin, _ := fake(t, `echo '{"result":"{\"findings\":[],\"resolved\":[]}"}'`)
	out, _, err := (&Claude{Bin: bin}).Run(t.Context(), nil)
	if err != nil || string(out) != okOut {
		t.Fatalf("%s %v", out, err)
	}
	for name, body := range map[string]string{
		"empty":   `echo '{"result":""}'`,
		"garbage": `echo nope`,
	} {
		bin, _ := fake(t, body)
		if _, _, err := (&Claude{Bin: bin}).Run(t.Context(), nil); !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	bin, _ = fake(t, `echo '{"is_error":true,"result":"Claude usage limit reached"}'`)
	if _, _, err := (&Claude{Bin: bin}).Run(t.Context(), nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("is_error: %v", err)
	}
	bin, _ = fake(t, `echo "Rate limit exceeded" >&2; exit 1`)
	if _, _, err := (&Claude{Bin: bin}).Run(t.Context(), nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("rate: %v", err)
	}
	bin, _ = fake(t, `echo boom >&2; exit 1`)
	if _, _, err := (&Claude{Bin: bin}).Run(t.Context(), nil); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("plain failure: %v", err)
	}
	if _, _, err := (&Claude{Bin: "/nonexistent/claude"}).Run(t.Context(), nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("missing: %v", err)
	}
	bin, _ = fake(t, `sleep 5`)
	if _, _, err := (&Claude{Bin: bin, Timeout: 100e6}).Run(t.Context(), nil); err == nil {
		t.Error("timeout expected")
	}
}

const codexBody = `while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
echo '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":4}}'
printf '%s' '` + okOut + `' > "$out"
`

func TestCodexRun(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "sk-secret")
	t.Setenv("CODEX_HOME", "/ch")
	bin, log := fake(t, codexBody)
	out, u, err := (&Codex{Model: "gpt", Bin: bin}).Run(t.Context(), []byte("PACKET"))
	if err != nil || string(out) != okOut || u.InputTokens != 3 || u.OutputTokens != 4 {
		t.Fatalf("%s %+v %v", out, u, err)
	}
	l := readLog(t, log)
	for _, w := range []string{
		"[exec]", "[-C]", "[--skip-git-repo-check]", "[-s]", "[read-only]", "[--ephemeral]", "[--ignore-user-config]",
		"[--ignore-rules]", "[-m]", "[gpt]", "[--output-schema]", "[--json]", "[-o]", "[-]", "CODEX_HOME=/ch", "PACKET", "You are a code reviewer",
	} {
		if !strings.Contains(l, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Contains(l, "sk-secret") {
		t.Fatal("API key leaked")
	}
}

func TestCodexToolEventRejected(t *testing.T) {
	for _, ev := range []string{
		`{"type":"item.completed","item":{"type":"command_execution"}}`,
		`{"type":"item.started","item":{"type":"mcp_tool_call"}}`,
		`{"type":"item.completed","item":{"type":"file_change"}}`,
	} {
		bin, _ := fake(t, `while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
echo '`+ev+`'
printf '%s' '`+okOut+`' > "$out"
`)
		if _, _, err := (&Codex{Bin: bin}).Run(t.Context(), nil); !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("%s: %v", ev, err)
		}
	}
	bin, _ := fake(t, `echo '{"type":"turn.completed"}'`)
	if _, _, err := (&Codex{Bin: bin}).Run(t.Context(), nil); !errors.Is(err, ErrInvalidOutput) {
		t.Errorf("no final message: %v", err)
	}
	bin, _ = fake(t, `echo "Please login" >&2; exit 1`)
	if _, _, err := (&Codex{Bin: bin}).Run(t.Context(), nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("auth: %v", err)
	}
}

// An empty model omits the flag so the subscription CLI picks its default.
func TestEmptyModelOmitsFlag(t *testing.T) {
	for _, a := range CodexArgs("/c", "", "/s", "/o") {
		if a == "-m" {
			t.Fatal("codex -m with empty model")
		}
	}
	for _, a := range ClaudeArgs("") {
		if a == "--model" {
			t.Fatal("claude --model with empty model")
		}
	}
}

// A failing codex run reports the error event, not the first stdout line.
func TestCodexErrorMessageSurfaced(t *testing.T) {
	bin, _ := fake(t, `echo '{"type":"thread.started","thread_id":"x"}'
echo '{"type":"turn.failed","error":{"message":"model not supported"}}'
exit 1`)
	_, _, err := (&Codex{Bin: bin}).Run(t.Context(), nil)
	if err == nil || !strings.Contains(err.Error(), "model not supported") {
		t.Fatalf("%v", err)
	}
}

var _ Reviewer = (*Claude)(nil)
