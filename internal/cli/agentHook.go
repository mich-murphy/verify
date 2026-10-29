package cli

import (
	"fmt"

	"github.com/mich-murphy/verify/internal/agenthook"
)

func init() { register("agent-hook", runAgentHook) }

// runAgentHook exits 2 with a one-line reason on stderr to block a tool call.
// Everything else, including malformed input, is allowed.
func runAgentHook(env *Env) int {
	fs := env.Flags("agent-hook")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("agent-hook", ExitUsage, "usage: gate agent-hook [--json] < tool-call.json", "fix the arguments")
	}
	v := agenthook.Evaluate(env.Stdin)
	if v.Warning != "" && !env.JSON {
		_, _ = fmt.Fprintln(env.Stderr, "gate agent-hook: warning: "+v.Warning)
	}
	if v.Block {
		if env.JSON {
			return env.Emit(Result{Command: "agent-hook", Status: "blocked", Message: v.Reason, NextAction: "do not bypass hooks; fix what they report"}, ExitUsage)
		}
		_, _ = fmt.Fprintln(env.Stderr, "gate: blocked: "+v.Reason)
		return ExitUsage
	}
	if env.JSON {
		return env.Emit(Result{Command: "agent-hook", Status: "allowed", Message: v.Warning}, ExitOK)
	}
	return ExitOK
}
