# Execution boundary

`New(Config).Run(ctx, Command)` executes one argv command with a directory,
positive timeout, explicit environment and optional native stdin. It never
invokes a shell implicitly. `Config.Env` replaces inheritance; only variables
in `Config.Inherit` are copied from the parent. Bare executable names resolve
against absolute directories in the controlled PATH. Configuration is copied.

The result distinguishes a process that started (including a real exit 127)
from a start failure (`Started` is zero, `ExitCode` is -1). Stdout and stderr
are each captured to a 1 MiB default bound; `Truncated` and `Bytes` disclose
incomplete capture. Output may contain secrets and must not be published
without appropriate handling.

On macOS/Linux a command starts in its own process group. Timeout and
cancellation send TERM and then KILL with bounded reaping and settlement.
A parent that exits with live owned children fails; zombies are not live.
`Settled=false` means cleanup was incomplete. The runner rejects further work.
Check and verify durably create a `<worktree>.quarantine` marker under their
worktree lock **before** reset, audit, prepare, setup, or scenarios run. Only
ordinary return with every command settled removes the owned marker, even when
a command failed. If Gate crashes or cleanup is incomplete, later runs refuse
to reuse that path. Recovery requires inspecting owned processes and explicitly
removing the stale marker.

This is cooperative **foreground process-group** ownership, not a sandbox:
commands must not daemonize, leave the process group, or swallow child failures.
Process ancestry polling cannot guarantee containment against deliberate escape.
Only an owned group is signalled, never an unrelated process by name or port.
Native tests on both supported platforms are required for a two-platform claim.
