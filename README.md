# Verify

Verify is a local command-line tool for one developer working with AI coding agents.
It runs your repository's own prek hooks plus a few guards on the exact commit, asks
one fresh reviewer for a review, exercises the command-line behaviour you changed, and
pushes only the commit it checked. The author (you or your agent) fixes whatever
Verify finds. Verify only reports.

Verify picks a lane from the diff. `docs` is for documentation-only changes, `standard`
is the default, and `signoff` needs a human `verify decide approve`. Guards run before
hooks, and a blocking guard skips them. A passing check on an identical tree is reused.
A failed hook is one finding, `check:<stage>:<hook>`.

Verify is cooperative: its `verify/verified` status guards against forgetting, not
against a determined attacker. macOS and Linux, GitHub only.

> **Status:** v1 is implemented and dogfooded; see [STATUS.md](STATUS.md). The design
> is in [SPEC.md](SPEC.md).

## Install

```sh
go install github.com/mich-murphy/verify/cmd/verify@latest
```

Or, from a checkout:

```sh
go install ./cmd/verify    # into $GOBIN
make build                # or build bin/verify
```

You also need `git`, [`prek`](https://github.com/j178/prek) and, for shipping,
the `gh` CLI. Reviews use the `claude` or `codex` subscription CLIs, chosen by `VERIFY_REVIEWER`
(`auto`, `claude` or `codex`), `VERIFY_CLAUDE_MODEL` and `VERIFY_CODEX_MODEL`.

## Quickstart

1. Run `verify setup --apply`. It adds missing hooks to the selected YAML config
   (creating `.pre-commit-config.yaml` only when no config exists) and installs the
   Git hooks. Existing TOML configs are preserved with manual guidance. Verify needs
   no `verify.yaml`; add one only for facts it cannot derive (see SPEC.md §3).
2. Edit the prek config so it runs your formatter, linter and tests. `verify setup`
   tells you what is missing.
3. Commit, then:

   ```
   verify setup    # audit the repo's quality setup; prints notes and fixes, edits nothing
   verify check    # guards, setup audit and your hooks, on the exact commit
   verify review   # one fresh review of the change
   verify ship     # push the commit, open the PR, set verify/verified
   ```

Every command accepts `--json`. Non-zero results include a `next_action`.
Exit codes: `0` passed, `1` blocking findings, `2` usage or config error,
`3` blocked (missing prerequisite, stale SHA, unavailable tool).

## Commands

| Command | Purpose |
|---|---|
| `verify check [--sha REF] [--fast]` | Run guards, the setup audit and repository hooks. Reuses a passing check for an identical tree. |
| `verify review [--intent FILE] [--reviewer auto\|claude\|codex]` | One fresh review, or a delta review after a repair. |
| `verify verify` | Run configured CLI scenarios when a declared surface changed. |
| `verify ship [--dry-run] [--remote NAME]` | Push the checked commit, open or update the PR (opened ready, with a title and description composed from the commits and diff by the reviewer CLI, or from the commit messages when it is unavailable), set `verify/verified`. |
| `verify decide approve\|accept ID\|waive OBLIGATION --reason TEXT` | Record a human decision for the current commit. |
| `verify status` | Show the lane, results, open findings and the next action. Exits 1 when findings block and 3 when blocked. |
| `verify setup [--apply]` | Audit the quality setup with notes on hook scope, always-run and uncacheable hooks. Without prek config it prints the YAML that `--apply` would write. |
| `verify doctor` | Check tool availability and noninteractive gh/reviewer authentication status. |
| `verify agent-hook` | Read an agent tool call on stdin; exit 2 to block hook bypasses. |

## Measured

Timings, coverage, mutation score and the live reviewer run are in [STATUS.md](STATUS.md).

## Upgrading from Gate

This is a breaking rename, not a compatibility alias. Install `verify`, update scripts
and hooks that invoke `gate`, rename `gate.yaml` to `verify.yaml`, and change `GATE_*`
environment variables to `VERIFY_*` (including `GATE_AUTHOR` → `VERIFY_AUTHOR`). Update
scenario executable paths such as `.gate-bin/gate` to `.verify-bin/verify` where used.
Replace the Pi extension activation path with `integrations/pi/verify.ts`; do not load
both extensions. Update Claude Code hook commands too.

State now lives under `<git-common-dir>/verify/`, and JSON schemas use the `verify.`
prefix (for example `verify.result/1`). Existing `<git-common-dir>/gate/`
state is left untouched and is not migrated or reused. Keep it for historical review
budgets, decisions and quarantine markers; do not switch names to evade an
unresolved finding. Settle any interrupted Gate processes before running Verify,
and regenerate check, review and scenario evidence with Verify rather than copying records.

Update GitHub branch protection from `gate/verified` to `verify/verified` when adopting
the new executable; the old required check will not be emitted by Verify. These user
and repository settings are not changed automatically. `verify verify` retains the
existing `verify` subcommand for CLI scenarios.

## Evidence and review limits

- Full checks and verification run against an exact commit in a locked worktree.
  Changing tracked content during execution fails the run. `--fast` checks local
  edits, including untracked files, but never supplies shipping evidence.
- Base selection cannot authenticate itself from the candidate's config. Missing
  or inconsistent records fail closed; evidence from a different merge-base is stale.
- Human decisions change the effective assessment, not historical execution results.
  Failed repository commands remain blocking. A verify waiver covers only a current
  `inconclusive` result, never missing evidence or `no-go`.
- One fresh reviewer covers correctness, security and simplicity. Concrete security
  violations use the existing correctness-plus-repro contract; simplicity is advisory.
  A reviewer-written repro is a claim, not a test that Verify has executed.
- A review run makes at most two reviewer calls: the primary, then one fallback to the
  other family or one retry of invalid output. Nothing carries between runs, so a new
  commit starts fresh. Repeating a valid exact-commit review reuses it.
- Interrupted execution leaves a worktree ownership marker. Inspect/stop surviving
  processes before explicitly removing the marker named in the error; do not bypass
  it just to rerun checks. Older records missing required provenance must be removed
  and regenerated as directed; Verify does not migrate them.
- Shipping resolves the selected remote's push destination once for the lease and
  push, and targets that same GitHub repository for the PR and commit status. Local
  checks are not a claim that remote CI or merge validation passed.

The evidence, review-budget and onboarding contracts are defined in [SPEC.md](SPEC.md).
Small, opt-in reviewer experiments and their limits are in
[reviewer evaluation](docs/reviewer-evaluation.md).

## Branch protection

Verify sets a commit status named `verify/verified`. To make GitHub enforce it, in the
repository go to Settings, Branches, add a rule for your base branch, enable
"Require status checks to pass before merging", and add `verify/verified` as a
required check. Verify does not change this setting for you.

## Agent integration

`verify agent-hook` blocks `--no-verify`, `core.hooksPath` changes and edits to hook
or Verify config. It reduces noise; `verify ship` re-checks everything.

- Pi: the extension `integrations/pi/verify.ts` calls `verify agent-hook` on
  `tool_call` and `verify check --fast` on `agent_end`.
- Claude Code: a `PreToolUse` hook runs `verify agent-hook`, and a `Stop` hook runs
  `verify check --fast` through a wrapper that exits 2 on findings.

Setup for both is in [integrations/pi/README.md](integrations/pi/README.md).

## Development

See [DEVELOPMENT.md](DEVELOPMENT.md).

## License

[MIT](LICENSE). The reviewer fixtures are original synthetic examples, not copied
application code. Dependencies retain their respective licenses.
