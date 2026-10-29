# Verify integrations for agents

## Pi

Try it for one run (needs `verify` on `PATH`; no install):

```sh
pi -e /path/to/verify/integrations/pi/verify.ts
# non-interactive: pi -p --no-session --no-extensions -e integrations/pi/verify.ts --tools bash "<prompt>"
```

To keep it, copy or symlink `verify.ts` to `~/.pi/agent/extensions/verify.ts` (or the project's `.pi/extensions/`).

- `tool_call` pipes `{toolName, input}` to `verify agent-hook`; exit 2 returns `{block: true, reason}`.
- `agent_end` runs `verify check --fast --json`. On failure it notifies you and sends a bounded
  follow-up message listing blocking findings. After 3 consecutive failures it stops feeding back;
  a passing check resets the counter. Pi 1.4.0 can start more runs after `agent_end`, so this is
  a per-run check, not a final-idle signal.

The integration has been exercised against Pi 1.4.0 in an isolated model session and in the
installed-package harness below. The harness uses a real scratch-built Verify executable and tests
hook-bypass blocking, failure feedback, the three-message cap and reset after a passing check.
Commands use `ctx.cwd`, bounded output and a timeout. The isolated model session also exercised
failure feedback followed by a repair and settlement; this is not interactive-TUI verification.

Use a model supported by your subscription. Authentication readiness and process exit alone do
not prove a model session succeeded: inspect the actual response, tool and follow-up events.
Provider errors may be reported in the event stream even when the process exits zero.

Run the harness without a model call (Go and an installed Pi package are required):

```sh
PI_PACKAGE=/path/to/installed/@earendil-works/pi-coding-agent node --test integrations/pi/verify.test.cjs
```

## Claude Code

`verify agent-hook` accepts Claude Code's PreToolUse JSON directly. A Stop hook only feeds
back to the model on exit code 2, but `verify check --fast` exits 1 on findings, so wrap it.
Save as `verify-stop.sh` (executable):

```sh
#!/bin/sh
verify check --fast || exit 2
```

`.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash|Write|Edit|MultiEdit",
        "hooks": [{ "type": "command", "command": "verify agent-hook" }] }
    ],
    "Stop": [
      { "hooks": [{ "type": "command", "command": "./verify-stop.sh" }] }
    ]
  }
}
```

Stop hooks re-run when they block, so keep the check fast; the hook output goes to stderr for the model.
