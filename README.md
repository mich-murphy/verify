# Gate

Gate is a local command-line tool for one developer working with AI coding agents.
It checks every commit with your repository's own tools plus a few guards of its
own. It then asks one fresh reviewer for a review, exercises the command-line
behaviour you changed, and pushes only the exact commit it checked. The author
(you or your agent) fixes whatever Gate finds. Gate only reports.

Gate is cooperative: its `gate/verified` status guards against forgetting, not
against a determined attacker. macOS and Linux, GitHub only.

> **Status:** v1 is implemented and dogfooded; see [STATUS.md](STATUS.md). The design
> is in [SPEC.md](SPEC.md).

## Install

```sh
go install github.com/mich-murphy/verify/cmd/gate@latest
```

Or, from a checkout:

```sh
go install ./cmd/gate      # into $GOBIN
make build                # or build bin/gate
```

The repository/module is named `verify`; the executable remains `gate`.

You also need `git`, [`prek`](https://github.com/j178/prek) and, for shipping,
the `gh` CLI. Reviews use the `claude` or `codex` subscription CLIs, chosen by `GATE_REVIEWER`
(`auto`, `claude` or `codex`), `GATE_CLAUDE_MODEL` and `GATE_CODEX_MODEL`.

## Quickstart

1. Run `gate setup --apply`. It adds missing hooks to the selected YAML config
   (creating `.pre-commit-config.yaml` only when no config exists) and installs the
   Git hooks. Existing TOML configs are preserved with manual guidance. Gate needs
   no `gate.yaml`; add one only for facts it cannot derive (see SPEC.md §3).
2. Edit the prek config so it runs your formatter, linter and tests. `gate setup`
   tells you what is missing.
3. Commit, then:

   ```
   gate setup    # audit the repo's quality setup; prints fixes, edits nothing
   gate check    # guards, setup audit and your checks, on the exact commit
   gate review   # one fresh review of the change
   gate ship     # push the commit, open the PR, set gate/verified
   ```

Every command accepts `--json`. Non-zero results include a `next_action`.
Exit codes: `0` passed, `1` blocking findings, `2` usage or config error,
`3` blocked (missing prerequisite, stale SHA, parked branch, unavailable tool).

## Commands

| Command | Purpose |
|---|---|
| `gate check [--sha REF] [--fast]` | Run guards, the setup audit and repository checks. |
| `gate review [--intent FILE] [--reviewer auto\|claude\|codex]` | One fresh review, or a delta review after a repair. |
| `gate verify` | Run configured CLI scenarios when a declared surface changed. |
| `gate ship [--dry-run] [--remote NAME]` | Push the checked commit, open or update the PR, set `gate/verified`. |
| `gate decide approve\|accept ID\|waive OBLIGATION --reason TEXT` | Record a human decision for the current commit. |
| `gate status` | Show the lane, results, open findings and the next action. |
| `gate setup [--apply]` | Static audit of the quality setup, with guidance; `--apply` onboards the repo. |
| `gate doctor` | Check tool availability and noninteractive gh/reviewer authentication status. |
| `gate agent-hook` | Read an agent tool call on stdin; exit 2 to block hook bypasses. |

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
  A reviewer-written repro is a claim, not a test that Gate has executed.
- Each branch has an initial review and one changed-commit delta review. Each logical
  review may retry invalid output once; subprocess attempts are counted separately.
  Repeating a valid exact-commit review reuses it. The final permitted clean review
  can pass; a further required review parks the branch for explicit human decisions.
- Interrupted execution leaves a worktree ownership marker. Inspect/stop surviving
  processes before explicitly removing the marker named in the error; do not bypass
  it just to rerun checks. Older records missing required provenance must be removed
  and regenerated as directed; Gate does not migrate them.
- Shipping resolves the selected remote's push destination once for the lease and
  push, and targets that same GitHub repository for the PR and commit status. Local
  checks are not a claim that remote CI or merge validation passed.

The evidence, review-budget and onboarding contracts are defined in [SPEC.md](SPEC.md).
Small, opt-in reviewer experiments and their limits are in
[reviewer evaluation](docs/reviewer-evaluation.md).

## Branch protection

Gate sets a commit status named `gate/verified`. To make GitHub enforce it, in the
repository go to Settings, Branches, add a rule for your base branch, enable
"Require status checks to pass before merging", and add `gate/verified` as a
required check. Gate does not change this setting for you.

## Agent integration

`gate agent-hook` blocks `--no-verify`, `core.hooksPath` changes and edits to hook
or Gate config. It reduces noise; `gate ship` re-checks everything.

- Pi: the extension `integrations/pi/gate.ts` calls `gate agent-hook` on
  `tool_call` and `gate check --fast` on `agent_end`.
- Claude Code: a `PreToolUse` hook runs `gate agent-hook`, and a `Stop` hook runs
  `gate check --fast` through a wrapper that exits 2 on findings.

Setup for both is in [integrations/pi/README.md](integrations/pi/README.md).

## Development

See [DEVELOPMENT.md](DEVELOPMENT.md).

## License

[MIT](LICENSE). The reviewer fixtures are original synthetic examples, not copied
application code. Dependencies retain their respective licenses.
