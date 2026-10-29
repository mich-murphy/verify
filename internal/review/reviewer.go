package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/runner"
)

// DefaultTimeout bounds one reviewer subprocess.
const DefaultTimeout = 10 * time.Minute

// ErrUnavailable means the CLI is missing, unauthenticated or rate limited.
// No review budget is consumed.
var ErrUnavailable = errors.New("reviewer unavailable")

var (
	errUnsettled     = errors.New("reviewer process group did not settle")
	errReviewTimeout = errors.New("reviewer timed out")
)

// Usage is the token usage reported by the CLI, when present.
type Usage struct{ InputTokens, OutputTokens int }

// Reviewer runs one fresh review of a packet and returns the raw JSON answer.
type Reviewer interface {
	Name() string
	Run(ctx context.Context, packet []byte) ([]byte, Usage, error)
}

// Select picks the reviewer. choice is auto, claude or codex; auto uses the
// family opposite to author (claude or codex) and defaults to codex.
func Select(choice, author, claudeModel, codexModel string) (Reviewer, error) {
	switch choice {
	case "", "auto":
		if author == "codex" {
			return &Claude{Model: claudeModel}, nil
		}
		return &Codex{Model: codexModel}, nil
	case "claude":
		return &Claude{Model: claudeModel}, nil
	case "codex":
		return &Codex{Model: codexModel}, nil
	}
	return nil, fmt.Errorf("unknown reviewer %q (want auto, claude or codex)", choice)
}

var envAllow = []string{"HOME", "PATH", "LANG", "LC_ALL", "USER"}

// scratch is a private directory tree: cwd stays empty, tmp is TMPDIR and
// files holds anything Gate hands to the CLI.
type scratch struct{ root, cwd, tmp string }

func newScratch() (*scratch, error) {
	root, err := os.MkdirTemp("", "gate-review-")
	if err != nil {
		return nil, err
	}
	s := &scratch{root: root, cwd: filepath.Join(root, "cwd"), tmp: filepath.Join(root, "tmp")}
	for _, d := range []string{s.cwd, s.tmp} {
		if err := os.Mkdir(d, 0o700); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
	}
	return s, nil
}

func (s *scratch) close() { _ = os.RemoveAll(s.root) }

// env builds the allowlisted environment; everything else is dropped.
func (s *scratch) env(extra ...string) []string {
	out := []string{"TMPDIR=" + s.tmp}
	for _, k := range append(append([]string{}, envAllow...), extra...) {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// proc runs one no-tools reviewer in the scratch cwd with bounded capture and
// process-group cleanup, reusing the repository command runner's OS boundary.
const maxReviewerOutput = 1 << 20

func (s *scratch) proc(ctx context.Context, timeout time.Duration, bin string, args []string, stdin []byte, env []string) (stdout, stderr []byte, err error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	allowed := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		allowed[k] = v
	}
	r, err := runner.New(runner.Config{Env: allowed, OutputLimit: maxReviewerOutput})
	if err != nil {
		return nil, nil, err
	}
	res, err := r.Run(ctx, runner.Command{Args: append([]string{bin}, args...), Dir: s.cwd, Stdin: stdin, Timeout: timeout})
	if err != nil {
		return nil, nil, err
	}
	stdout, stderr = []byte(res.Stdout.Output), []byte(res.Stderr.Output)
	if !res.Settled {
		return stdout, stderr, fmt.Errorf("%w: %s", errUnsettled, res.Error)
	}
	if res.Status == runner.TimedOut || res.Status == runner.Cancelled {
		return stdout, stderr, fmt.Errorf("%w after %s", errReviewTimeout, timeout)
	}
	if res.Status != runner.Pass {
		if res.Started.IsZero() {
			return stdout, stderr, fmt.Errorf("%w: %s could not start", ErrUnavailable, bin)
		}
		return stdout, stderr, fmt.Errorf("%s failed: %s", bin, res.Error)
	}
	if res.Stdout.Truncated || res.Stderr.Truncated {
		return stdout, stderr, invalid("%s output exceeded %d bytes", bin, maxReviewerOutput)
	}
	return stdout, stderr, nil
}

// classify turns a failed run into ErrUnavailable or a plain error.
func classify(name string, err error, texts ...[]byte) error {
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s is not installed", ErrUnavailable, name)
	}
	all := strings.ToLower(string(bytes.Join(texts, []byte("\n"))))
	for _, p := range []string{"rate limit", "rate-limit", "usage limit", "quota", "login", "log in", "auth", "unauthorized", "429"} {
		if strings.Contains(all, p) {
			return fmt.Errorf("%w: %s: %s", ErrUnavailable, name, firstLine(all))
		}
	}
	return fmt.Errorf("%s failed: %w: %s", name, err, firstLine(all))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// Claude runs `claude -p` with no tools, settings or session.
type Claude struct {
	Model   string
	Bin     string // default "claude"
	Timeout time.Duration
}

// Name implements Reviewer.
func (c *Claude) Name() string { return "claude" }

// ClaudeArgs is the argv after the binary name.
func ClaudeArgs(model string) []string {
	args := []string{"-p", "--output-format", "json", "--json-schema", Schema}
	if model != "" {
		args = append(args, "--model", model)
	}
	return append(args,
		"--tools", "", "--setting-sources", "", "--strict-mcp-config", "--disable-slash-commands",
		"--no-session-persistence", "--system-prompt", SystemPrompt,
	)
}

// Run implements Reviewer.
func (c *Claude) Run(ctx context.Context, packet []byte) ([]byte, Usage, error) {
	s, err := newScratch()
	if err != nil {
		return nil, Usage{}, err
	}
	defer s.close()
	bin := orDefault(c.Bin, "claude")
	stdout, stderr, err := s.proc(ctx, c.Timeout, bin, ClaudeArgs(c.Model), packet, s.env("CLAUDE_CONFIG_DIR"))
	if err != nil {
		if errors.Is(err, ErrInvalidOutput) {
			return nil, Usage{}, err
		}
		if errors.Is(err, errUnsettled) || errors.Is(err, errReviewTimeout) {
			return stdout, Usage{}, err
		}
		return nil, Usage{}, classify("claude", err, stderr, stdout)
	}
	var w struct {
		IsError          bool            `json:"is_error"`
		Result           json.RawMessage `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(stdout, &w); err != nil {
		return nil, Usage{}, invalid("claude output is not JSON: %v", err)
	}
	u := Usage{InputTokens: w.Usage.In, OutputTokens: w.Usage.Out}
	var text string
	_ = json.Unmarshal(w.Result, &text)
	if w.IsError {
		return nil, u, classify("claude", errors.New("is_error"), []byte(text), stderr)
	}
	if so := bytes.TrimSpace(w.StructuredOutput); len(so) > 0 && so[0] == '{' {
		return so, u, nil
	}
	if text == "" {
		return nil, u, invalid("claude result is empty")
	}
	return []byte(text), u, nil
}

// Codex runs `codex exec` read-only and ephemeral.
type Codex struct {
	Model   string
	Bin     string // default "codex"
	Timeout time.Duration
}

// Name implements Reviewer.
func (c *Codex) Name() string { return "codex" }

// CodexArgs is the argv after the binary name.
func CodexArgs(cwd, model, schemaFile, outFile string) []string {
	args := []string{
		"exec", "-C", cwd, "--skip-git-repo-check", "-s", "read-only", "--ephemeral",
		"--ignore-user-config", "--ignore-rules",
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	return append(args, "--output-schema", schemaFile, "--json", "-o", outFile, "-")
}

// codexError returns the last error message in codex's JSONL event stream,
// so failures report the cause rather than the first event.
func codexError(stdout []byte) []byte {
	var msg string
	for line := range bytes.SplitSeq(stdout, []byte("\n")) {
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "error" && ev.Message != "":
			msg = ev.Message
		case ev.Type == "turn.failed" && ev.Error.Message != "":
			msg = ev.Error.Message
		}
	}
	return []byte(msg)
}

// Run implements Reviewer. The system prompt is prepended to stdin because
// codex exec has no system-prompt flag.
func (c *Codex) Run(ctx context.Context, packet []byte) ([]byte, Usage, error) {
	s, err := newScratch()
	if err != nil {
		return nil, Usage{}, err
	}
	defer s.close()
	schemaFile, outFile := filepath.Join(s.root, "schema.json"), filepath.Join(s.root, "out.json")
	if err := os.WriteFile(schemaFile, []byte(Schema), 0o600); err != nil {
		return nil, Usage{}, err
	}
	stdin := append([]byte(SystemPrompt+"\n\n=== PACKET ===\n"), packet...)
	stdout, stderr, err := s.proc(ctx, c.Timeout, orDefault(c.Bin, "codex"),
		CodexArgs(s.cwd, c.Model, schemaFile, outFile), stdin, s.env("CODEX_HOME"))
	if err != nil {
		if errors.Is(err, ErrInvalidOutput) {
			return nil, Usage{}, err
		}
		if errors.Is(err, errUnsettled) || errors.Is(err, errReviewTimeout) {
			return stdout, Usage{}, err
		}
		return nil, Usage{}, classify("codex", err, stderr, codexError(stdout), stdout)
	}
	u, err := scanEvents(stdout)
	if err != nil {
		return nil, u, err
	}
	f, err := os.Open(outFile) // #nosec G304 -- Gate-created path in a private temp dir.
	if err != nil {
		return nil, u, invalid("codex wrote no final message")
	}
	defer func() { _ = f.Close() }()
	out, err := io.ReadAll(io.LimitReader(f, maxReviewerOutput+1))
	if err != nil || len(out) > maxReviewerOutput {
		return nil, u, invalid("codex final message exceeded output limit or could not be read")
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, u, invalid("codex wrote no final message")
	}
	return out, u, nil
}

// scanEvents rejects any tool-ish event in codex's JSONL stream and sums usage.
func scanEvents(stdout []byte) (Usage, error) {
	var u Usage
	for line := range bytes.SplitSeq(stdout, []byte("\n")) {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
			Usage struct {
				In  int `json:"input_tokens"`
				Out int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		for _, t := range []string{ev.Type, ev.Item.Type} {
			for _, bad := range []string{"command", "exec", "tool", "file_change", "patch", "mcp"} {
				if strings.Contains(strings.ToLower(t), bad) {
					return u, invalid("codex emitted a %q event; the reviewer must not use tools", t)
				}
			}
		}
		u.InputTokens += ev.Usage.In
		u.OutputTokens += ev.Usage.Out
	}
	return u, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
