// Package cli parses arguments, dispatches commands and renders results.
// Each command lives in its own file and registers itself with register, so
// the dispatch table never needs editing when a command is replaced.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mich-murphy/verify/internal/state"
)

// Exit codes (SPEC §2).
const (
	ExitOK       = 0
	ExitFindings = 1
	ExitUsage    = 2
	ExitBlocked  = 3
)

// ResultSchema identifies the --json output.
const ResultSchema = "gate.result/1"

// Result is the single object printed by --json.
type Result struct {
	Schema     string          `json:"schema"`
	Command    string          `json:"command"`
	SHA        string          `json:"sha,omitempty"`
	Lane       string          `json:"lane,omitempty"`
	Status     string          `json:"status"`
	Steps      []state.Step    `json:"steps"`
	Findings   []state.Finding `json:"findings"`
	NextAction string          `json:"next_action,omitempty"`
	Message    string          `json:"message,omitempty"`
}

// Env is everything a command needs.
type Env struct {
	Ctx    context.Context
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	JSON   bool
	Dir    string
	help   string
}

// Command runs with parsed-nothing Env.Args (flags after the command name).
type Command func(env *Env) int

var commands = map[string]Command{}

func register(name string, c Command) { commands[name] = c }

const usageText = `usage: gate <command> [flags]

commands:
  check [--sha REF] [--fast]   run guards, setup audit and repository checks
  review                       run one fresh review
  verify                       run CLI scenarios for changed surfaces
  ship [--dry-run]             push the checked commit and open the PR
  decide                       record a human decision
  status                       show lane, results, findings and next action
  setup                        run the static setup audit
  doctor                       check required tools
  agent-hook                   screen an agent tool call on stdin

every command accepts --json.
`

// Run dispatches args and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// Detect JSON before flag parsing: flag.Parse stops at the first unknown flag.
	// In particular, `gate check --bad --json` must still emit one JSON result.
	jsonMode := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "--json=true" {
			jsonMode = true
		}
	}
	env := &Env{Args: args, Stdin: stdin, Stdout: stdout, Stderr: stderr, JSON: jsonMode}
	if len(args) == 0 || args[0] == "--json" && len(args) == 1 {
		return env.Fail("gate", ExitUsage, usageText, "choose a command from gate --help")
	}
	if args[0] == "--json" {
		args = args[1:]
	}
	switch args[0] {
	case "-h", "--help", "help":
		if jsonMode {
			return env.Emit(Result{Command: "help", Status: "ok", Message: usageText}, ExitOK)
		}
		_, _ = io.WriteString(stdout, usageText)
		return ExitOK
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return env.Fail(args[0], ExitUsage, fmt.Sprintf("unknown command %q\n\n%s", args[0], usageText), "choose a command from gate --help")
	}
	env.Args = args[1:]
	dir, err := os.Getwd()
	if err != nil {
		return env.Fail(args[0], ExitBlocked, err.Error(), "run gate from an accessible directory")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env.Ctx, env.Dir = ctx, dir
	return cmd(env)
}

// Flags returns a FlagSet with the shared --json flag bound to env.JSON.
func (e *Env) Flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("gate "+name, flag.ContinueOnError)
	var diagnostics bytes.Buffer
	if e.JSON {
		fs.SetOutput(&diagnostics) // JSON mode has exactly one result, including on parse errors.
	} else {
		fs.SetOutput(io.MultiWriter(e.Stderr, &diagnostics))
	}
	fs.BoolVar(&e.JSON, "json", e.JSON, "print one gate.result/1 JSON object")
	fs.Usage = func() {
		// The flag package also calls Usage after a parse error. Only ErrHelp
		// calls it without first writing a diagnostic to FlagSet.Output().
		if diagnostics.Len() != 0 {
			return
		}
		var text bytes.Buffer
		if name == "decide" {
			_, _ = fmt.Fprintln(&text, "usage: gate decide approve|accept ID|waive OBLIGATION [flags]")
		} else {
			_, _ = fmt.Fprintf(&text, "usage: gate %s [flags]\n", name)
		}
		original := fs.Output()
		fs.SetOutput(&text)
		fs.PrintDefaults()
		fs.SetOutput(original)
		e.help = text.String()
	}
	return fs
}

// Parse parses e.Args; ok is false when the caller should return ExitUsage.
func (e *Env) Parse(fs *flag.FlagSet) (ok bool) {
	if err := fs.Parse(e.Args); err != nil {
		return false
	}
	return true
}

// Emit prints res (text or JSON) and returns code.
func (e *Env) Emit(res Result, code int) int {
	res.Schema = ResultSchema
	if res.Steps == nil {
		res.Steps = []state.Step{}
	}
	if res.Findings == nil {
		res.Findings = []state.Finding{}
	}
	if e.JSON {
		enc := json.NewEncoder(e.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		return code
	}
	renderText(e.Stdout, res)
	return code
}

// Fail emits an error-ish result with next_action.
func (e *Env) Fail(command string, code int, msg, next string) int {
	if e.help != "" {
		return e.Emit(Result{Command: command, Status: "ok", Message: e.help}, ExitOK)
	}
	status := "blocked"
	if code == ExitUsage {
		status = "usage"
	}
	return e.Emit(Result{Command: command, Status: status, Message: msg, NextAction: next}, code)
}

func renderText(w io.Writer, r Result) {
	head := "gate " + r.Command + ": " + r.Status
	if r.Lane != "" {
		head += " [" + r.Lane + " lane]"
	}
	if r.SHA != "" {
		head += " " + r.SHA[:min(12, len(r.SHA))]
	}
	_, _ = fmt.Fprintln(w, head)
	for _, s := range r.Steps {
		_, _ = fmt.Fprintf(w, "  %-4s %s (exit %d, %dms)\n", s.Status, s.Name, s.ExitCode, s.DurationMS)
	}
	for _, f := range r.Findings {
		loc := ""
		if f.Path != "" {
			loc = fmt.Sprintf(" %s:%d", f.Path, f.Line)
		}
		_, _ = fmt.Fprintf(w, "  %s %s [%s]%s %s\n", f.Severity, f.ID, f.State, loc, f.Message)
	}
	if r.Message != "" {
		_, _ = fmt.Fprintln(w, strings.TrimRight(r.Message, "\n"))
	}
	if r.NextAction != "" {
		_, _ = fmt.Fprintln(w, "next: "+r.NextAction)
	}
}
