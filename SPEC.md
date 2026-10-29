# Gate v1 — specification

Gate is a local CLI for one developer working with AI coding agents. It checks every
commit using the repository's own tools plus a few guards that Gate owns. It then asks one
fresh reviewer once, exercises changed CLI surfaces, and pushes only the exact commit it
checked. The author agent fixes whatever Gate finds; Gate only reports.

This is the normative specification for the public implementation. Tests and original synthetic
review examples live in this repository; development archives are not part of the public release.

## 1. Principles

1. Deterministic checks run first on every commit, scoped to the change's blast radius (§4.2);
   CI runs them in full. No lane skips them.
2. Validate the exact commit you push. Every record is bound to a commit SHA.
3. Evidence comes from Gate's own runs, never from an agent's report.
4. Judge and worker are separate. The reviewer is a fresh call with no tools, from a different
   model family by default. It never sees test results or the author's reasoning.
5. The author fixes; Gate never edits source, commits, rebases or merges.
6. Every loop has a budget. Requiring another review beyond the budget parks the branch;
   shipping a parked branch requires explicit human decisions for the current SHA.
7. Silence by default. Block only on the short list in §8; everything else is a note.
8. Repository config is code. Changes to quality config raise the lane; they are not silently trusted.
9. Start with the smallest version and let measurements add the rest.

Threat model: Gate is cooperative. The `gate/verified` status is a guard against forgetting, not
against a determined attacker. Gate does not protect against the authoring session.

Platforms are macOS and Linux. GitHub is the only host.

Out of scope: daemon/proxy, Gate-made commits, rebase, CI watching, unbounded review loops or
additional reviewer stages, TUI/DB, Windows, hostile-code sandboxing, and billed model APIs. The reviewer uses subscription
CLIs.

## 2. Commands

| Command | Purpose |
|---|---|
| `gate check [--sha REF] [--fast]` | Run guards, the setup audit and the repository checks on the exact commit (§4). |
| `gate review [--intent FILE] [--reviewer auto\|claude\|codex]` | Run one fresh review, or a delta review after a repair (§5). |
| `gate verify` | Run the configured CLI scenarios when a declared surface changed (§6). |
| `gate ship [--dry-run] [--remote NAME]` | Push the exact commit (to `origin` by default), open or update the PR, and set the `gate/verified` status (§7). |
| `gate decide approve\|accept ID\|waive OBLIGATION --reason TEXT` | Record a human decision for the current SHA. |
| `gate status` | Show the lane, step results, open findings, budgets and the next action for HEAD. |
| `gate setup [--apply]` | Run the static setup audit (§4.3) and print guidance. `--apply` onboards the repository: it writes what is missing and installs the hooks (§4.3.1). |
| `gate doctor` | Check that git, prek, gh and the reviewer CLIs are available and authenticated, without printing secrets. |
| `gate agent-hook` | Read an agent tool-call JSON on stdin and exit 2 to block hook bypasses (§9). |

Every command accepts `--json` and prints one `gate.result/1` object. Exit codes:

| Code | Meaning |
|---|---|
| `0` | Passed or OK. |
| `1` | Blocking findings. |
| `2` | Usage or configuration error. |
| `3` | Blocked. Covers a missing prerequisite, a stale SHA, a parked branch, or an unavailable tool or reviewer. |

Every non-zero result carries `next_action`.

## 3. Configuration and state

### Configuration

Gate needs no configuration file. Everything that can be derived is derived:

| Setting | Source |
|---|---|
| Base branch | `origin/HEAD`; otherwise `main` |
| Profile | Root files: `go.mod` is go; `package.json` is typescript; both is mixed |
| Stages | Fixed: `pre-commit`, then `pre-push` (§4.5) |
| Prepare | The root lockfile, else the single immediate subdirectory holding one (run there with the manager's directory flag): `npm ci`, `pnpm install --frozen-lockfile`, `bun install --frozen-lockfile` or `yarn install --immutable`; none for Go |
| Reviewer | The person running Gate, through the environment: `GATE_REVIEWER` (`auto`, `claude` or `codex`; default `auto`), `GATE_CLAUDE_MODEL` (default `opus`), `GATE_CODEX_MODEL` (default: the CLI's own) |
| Limits | Fixed: 2 logical reviews per branch, each with at most one invalid-output retry (§5); Sign-off above 400 changed lines |

An optional `gate.yaml` in the repository root holds only facts about the repository that cannot
be derived. Every key is optional, and an unknown key is an error:

```yaml
base: main                    # only when origin/HEAD is not the target branch
prepare:                      # only when the lockfile does not describe the install
  - name: deps
    run: [bun, install, --cwd, web, --frozen-lockfile]
    timeout: 10m
signoff_paths: ["internal/state/**"]   # extra critical paths
verify:                       # CLI scenarios (§6)
  surfaces: ["cmd/**"]
  setup: [go, build, -o, .gate-bin/gate, ./cmd/gate]
  scenarios:
    - name: help
      run: [.gate-bin/gate, --help]
      exit: 0
      stdout_contains: "usage"
waivers:                      # setup obligations waived, each with a reason
  - {obligation: deadcode, reason: "..."}
```

Policy discovery starts at the merge-base with the derived default branch, independently of
candidate config. A valid `gate.yaml` there governs, including its explicit `base`. Only when
that trusted config is absent or invalid may the candidate's copy govern; this requires Sign-off.
An explicit fallback target cannot select the candidate itself and hide the change. Gate computes
merge-bases against local `base` and `origin/<base>` where available. It uses the remote-derived
result when that advances the local-derived result; otherwise it retains the local-derived result
(or the sole available result). Repository checks belong in the prek config, not in `gate.yaml`.

Configuration is a single YAML document. Commands require a nonempty executable and no NUL
arguments; later argv elements may be empty strings. Scenario names are unique, expected exits
are 0–255, and declared verification surfaces require at least one scenario.

Prek config discovery is shared by audit, checks and onboarding, in this order: `prek.toml`,
`.pre-commit-config.yaml`, `.pre-commit-config.yml`. Gate does not create a competing config.

### State

State lives under `<git-common-dir>/gate/`. Linked worktrees share it, and it is never committed.

| Path | Holds |
|---|---|
| `branches/<branch>.json` | `gate.branch/1`: logical reviews used, subprocess attempts, persistent review findings (Gate-assigned IDs `F1`, `F2`, …, with state `open`, `resolved` or `accepted`), the last reviewed SHA, the parked flag, and decisions. |
| `runs/<sha>/check.json` | `gate.check/1` record. |
| `runs/<sha>/review.json` | `gate.review/1` record. |
| `runs/<sha>/verify.json` | `gate.verify/1` record. |
| `runs/<sha>/logs/<step>.log` | Step output. |
| `lock` | Lock file. |

Writes are atomic (temp file, fsync, rename) and are made while holding the lock. Branch and log
names are reversibly escaped to avoid collisions. Every record has version `/1`; there are no
migrations. A corrupt, unreadable or semantically inconsistent record fails closed with a clear
message. Older records missing required provenance must be removed and regenerated.

Check and verify records bind the SHA and merge-base; reviews also bind the branch. Check
admission requires the current lane. Stale evidence cannot qualify the current change. Human
decisions change the effective assessment, not the recorded execution result. Status, review
admission and shipping apply that decision-aware assessment. The PR body preserves raw execution
results while showing effective finding dispositions and human decisions. A failed repository
command cannot be made passing by accepting a finding.

### Lanes

The lane is a pure function of the diff from the merge-base to the SHA, plus the config.

| Lane | Applies when | Requires |
|---|---|---|
| Docs | Every changed path is `*.md`, under `docs/**`, or `CHANGELOG*`. | check |
| Standard | Neither Docs nor Sign-off applies. | check, review, and verify if a surface changed |
| Sign-off | The diff touches `gate.yaml`, prek or quality config (prek config, `.golangci*`, `tsconfig*`, linter, formatter and coverage configs, CI workflows), lock files with removed entries, or `signoff_paths`; or it changes more than 400 lines. | Standard's steps plus `gate decide approve` on that SHA |

## 4. Check

`gate check` resolves the SHA (HEAD by default), the merge-base with `base` and the lane. It then
runs these steps in the order given in §4.1–§4.4.

All steps except the guards run in a Gate-owned detached worktree of the exact SHA. Full checks
do not check the user's uncommitted changes; `--fast` does (§4.5). If the working tree differs
from the SHA, Gate prints a note. Repository execution must leave HEAD, the index and tracked
content at the checked SHA; changing tracked content fails the run rather than producing evidence
for the original commit.

The worktree is reused: `check` always uses `<git-common-dir>/gate/worktrees/check` (and `verify`
uses `.../verify`), held under an exclusive lock file next to it, so concurrent runs in one
repository wait rather than collide. Each run resets it to the SHA with `git checkout --detach
--force <sha>` and `git clean -ffdx -e node_modules -e .turbo`, so it holds exactly the commit's
tracked files plus installed dependencies and content-keyed tool caches. If the directory is missing or is not a valid worktree, Gate
removes it and adds it again. A stable path and unchanged file times keep tool caches valid, such
as Go's test cache, which keys on both. `prepare:` still runs every time; a frozen-lockfile
install over existing dependencies is fast.

Commands use one shared foreground runner with stdin, a timeout, bounded capture and process-group
cleanup. Repository commands inherit the user's environment with `GIT_*` variables removed;
reviewers use their separate allowlist (§5). A missing tool fails the step, never passes it.

Before reset or repository execution, Gate writes a durable worktree ownership marker with a
unique per-acquisition token. It removes the marker only after execution has settled and both
file identity and bounded marker contents still match; inode identity alone can be reused. Interrupted
or unsettled execution refuses later worktree reuse. Recovery is explicit: inspect and stop any
surviving processes before removing the marker named in the error, not merely to force a retry.

### 4.1 Guards

Guards run in-process over `git diff merge-base..sha`.

| Guard | Fires when | Effect |
|---|---|---|
| `secrets` | betterleaks, linked into Gate as a Go library with its default rules, finds a secret in the commit range. No external scanner is needed. | Block |
| `suppressions` | Added lines contain a lint or type suppression, a skipped test or a focused test (§4.1.1). | Block |
| `tests-removed` | A test file is deleted, or test functions or cases are removed, without a replacement. | Block |
| `generated` | The diff edits a file whose header marks it generated (`Code generated … DO NOT EDIT`, `@generated`). | Block |
| `config` | The diff changes quality config. | Raises the lane to Sign-off and produces a note. |

#### 4.1.1 Suppression patterns

The `suppressions` guard matches these patterns on added lines:

- Lint suppressions: `//nolint`, `eslint-disable`, `oxlint-disable`, `biome-ignore`, `# noqa`.
- Type suppressions: `@ts-ignore`, `@ts-expect-error`, `@ts-nocheck`.
- Skipped or focused tests: `t.Skip(`, `.skip(`, `.only(`, `xit(`, `xdescribe(`.

The finding goes away when a `gate decide accept` on that SHA references the guard finding ID.

### 4.2 Repository checks

Gate first runs each `prepare:` entry in the worktree (for example `npm ci`, because a fresh
worktree has no `node_modules`). A failing prepare step is a blocking `check:prepare-<name>`
finding, and the remaining steps are skipped.

Checks run only on the change's blast radius. Gate computes the scope once per run:

- `full` when there is no merge-base, or when the change touches a file that can affect
  everything: a package manifest or lockfile (`go.mod`, `go.sum`, `package.json`, any lockfile),
  quality config (the Sign-off list in §3, which includes the prek config and CI workflows),
  `gate.yaml`, or a task-runner file (`Makefile`, `justfile`, `Taskfile*`, `mise.toml`).
- `affected` otherwise.

For each stage, Gate runs `prek run --from-ref <merge-base> --to-ref <sha> --stage <stage>` when
the scope is `affected`, and `prek run --all-files --stage <stage>` when it is `full`, in the
worktree and with the worktree's own prek config. On a scoped run prek runs only the hooks whose
`files`/`types` match a changed file (plus `always_run` hooks), and exports
`PRE_COMMIT_FROM_REF` and `PRE_COMMIT_TO_REF` to them. Git's own pre-push hook sets the same
variables, so one hook definition behaves the same under `git push` and `gate check`. Gate sets no
variables of its own.

The check record holds `scope` and, for `full`, the reason (`scope_reason`). `ship` accepts
either scope: a scoped check is fast local evidence, and CI stays complete before merge. Every
step records its exit code, its duration and a log path. A non-zero exit is a blocking finding
that names the step.

If the repository has no prek config, the setup audit reports it as a blocking `setup:prek-config`
finding.

### 4.3 Setup audit

The setup audit checks whether the repository's quality setup meets the expectation profile.
Go and TypeScript obligations are listed below; a mixed repository must satisfy both profiles.

Each obligation is REQUIRED or RECOMMENDED. The audit learns which hooks exist, and at which
stages, from `prek list --output-format json`, and reads tool config files (`.golangci.yml`,
`go.mod`, `tsconfig*.json`, lockfiles). It runs nothing else, and it never reads Makefiles or
package scripts.

**Hook obligations** are met by a hook whose ID names the obligation, at the `pre-commit` or
`pre-push` stage (`manual` does not count). An ID matches when it equals the standard ID or an
accepted alternative, or has one as a `-`-separated prefix or suffix (`web-oxlint`,
`golangci-lint-full`, `tidy-check`):

| Obligation | Standard ID | Also accepted | Profile |
|---|---|---|---|
| format | `format` | `fmt`, `gofumpt`, `oxfmt`, `prettier`, `biome-format`, `dprint` | both |
| lint | `lint` | `golangci-lint`, `oxlint`, `eslint`, `biome-lint` | both |
| tidy | `tidy` | `go-mod-tidy` | Go |
| typecheck | `typecheck` | `tsc`, `tsgo`, `type-check`, `check-types` | TypeScript |
| test | `test` | `tests`, and any coverage hook (it runs the tests) | both |
| coverage | `coverage` | `cover` | Go REQUIRED, TypeScript RECOMMENDED |
| deadcode | `deadcode` | `dead-code`, `fallow`, `knip` | Go REQUIRED, TypeScript RECOMMENDED |
| vuln | `vuln` | `govulncheck` | Go |
| actionlint | `actionlint` | | when workflows exist: Go REQUIRED, TypeScript RECOMMENDED |
| fix | `fix` | `go-fix` | Go RECOMMENDED |
| dep-audit | `dep-audit` | `audit`, `npm-audit` | TypeScript RECOMMENDED |

If `prek list` fails (for example, a remote hook repository cannot be fetched on a cold cache),
the audit reports a blocking `setup:prek-config` finding that carries prek's error.

Gate does not inspect what a hook runs: no tool or registry can certify that a hook named `lint`
lints. The assurance is that a change to the prek config is a Sign-off change (§3), so a person
approves what each hook runs, and `gate check` runs every hook. `gate setup` prints every hook that met
each obligation, so a mislabelled or wrongly matched hook is visible.

**File obligations** are detected from the files themselves:

- Go, REQUIRED: golangci-lint v2 config with the common-core linters (govet, staticcheck, errcheck, errorlint, ineffassign, unused, gosec,
  revive, gocritic, nolintlint with an explanation requirement, gocognit); a pinned Go version in
  `go.mod`.
- Go, RECOMMENDED: fuzz targets.
- TypeScript, REQUIRED: `tsconfig` with `strict` and `noUncheckedIndexedAccess`; a linter config
  (`.oxlintrc.json`, `eslint.config.*`, `biome.json`), or the project's `lint` script flag
  (`--report-unused-disable-directives-severity=error`), that reports unused disable directives
  as errors; a lockfile and exact tool versions.
- TypeScript, RECOMMENDED: `exactOptionalPropertyTypes`.
- Common, REQUIRED: a prek config (`setup:prek-config`).
- Common, RECOMMENDED: builtin hygiene hooks; a CI workflow.

A TypeScript project in a single nested directory (for example `web/`) is audited there when the
root has no lockfile.

Secret scanning is not an obligation, because the `secrets` guard runs inside Gate on every check.

A missing REQUIRED obligation is a blocking `setup:<obligation>` finding. It is cleared by fixing
the config, or by a `waivers:` entry with a reason (which makes the change a Sign-off change). A
missing RECOMMENDED obligation is a note.

`gate setup` prints the same audit with concrete config snippets.

#### 4.3.1 Onboarding

`gate setup --apply` onboards a repository. It fills in only what is missing and never overwrites
or reorders existing content:

- It never writes `gate.yaml`.
- It writes `.pre-commit-config.yaml` when no supported prek config exists. For a safely appendable
  existing YAML/YML config, it appends missing `repo: local` hooks with standard IDs. Existing
  TOML, or YAML whose layout cannot be extended safely, is preserved with manual guidance;
  Gate does not rewrite it or create an alternative config. Stages and scoping follow §4.5.
- Each hook calls an existing entry point found by name only, without reading what it runs: a
  Make target, or a package script in the root or the single nested project (called with the
  manager's directory flag, `bun run --cwd web check-types`), whose name is the standard ID or an
  accepted alternative. For `format` and `tidy`, whose plain entry points usually fix files
  (`make fmt`, `npm run format`), only a check-named entry point counts (`fmt-check`,
  `format:check`, `check-format`). A `coverage`/`cover` entry point is written as the coverage hook, which
  also meets the test obligation, so no separate test hook is written.
- Otherwise it uses the starter command. A JavaScript starter is written only when its tool is a
  declared dependency; otherwise the hook is printed, not written, because `npx` would fetch an
  unrelated package of the same name.
- The written hooks are a starting point that the author adjusts; the Sign-off approval covers
  what they run.
- It installs the Git hook shims with `prek install --hook-type pre-commit --hook-type pre-push`.

An obligation that needs a code or tool-config change, such as enabling
`noUncheckedIndexedAccess`, is printed rather than applied, because it can break the build. After
applying, Gate reruns the audit and prints what remains. The written files are ordinary changes
that the author commits. Because they add prek config, the onboarding change is in the Sign-off
lane.

### 4.4 Revert test (later)

For bug-fix intent, new tests must fail on the base commit with an assertion. This step is
deferred until intent capture exists.

### 4.5 Hook tiers

This is the standard for which checks run at which prek stage. `gate setup --apply` writes hooks
to it. The audit reports a `setup:hook-tiers` note when a full-tier hook (typecheck, test,
coverage, deadcode, vuln) runs at `pre-commit`; it does not inspect `files`/`types` scoping.

| Stage | Budget | Contents |
|---|---|---|
| `pre-commit` (fast tier) | Median under 5 s on a typical change | Hygiene (prek `repo: builtin` hooks), formatter in check mode, linter, secrets on the staged diff, repository rule scripts |
| `pre-push` (full tier) | About 20 s on a typical change | Type check, tests (`-race` for Go), coverage floor, dead code, vulnerability scan, module tidiness |

Rules for the fast tier:

1. **Scope to the change.** File-level tools (formatters, oxlint, hygiene) receive filenames.
   Package-level tools (golangci-lint) run once with `pass_filenames: false`, and use `types` or
   `files` so that they fire only when a relevant file is staged. `always_run: true` is for
   `pre-push` only.
2. **Run in parallel.** Independent hooks share one prek `priority`, so prek runs them
   concurrently. Hygiene uses prek's `builtin` hooks, which need no cloned repository or Python
   environment.
3. **Check, do not fix.** Formatters run in check mode. A fixer that changes a file fails the run,
   so the commit always holds exactly what was checked.
4. **Nothing slow.** Tests, type checking, coverage and vulnerability scans run in `pre-push`.

Rules for the full tier:

1. **Run each suite once.** One test run produces both the race result and the coverage profile;
   a separate `test` hook that reruns the suite is a duplicate.
2. **Stay cacheable.** Avoid flags that disable the tool's cache in hooks (for Go, `-count=1` and
   `-shuffle`); CI keeps them. Unchanged packages then cost almost nothing.
3. **Run in parallel.** Independent full-tier hooks share one prek `priority`.
4. **Scope to the blast radius.** Every hook declares `files` or `types` so that it runs only when
   a relevant file changed: Go tests, coverage, vet, deadcode and govulncheck on `types: [go]`
   (govulncheck's verdict depends on which functions the code calls, so a code change can expose
   an old advisory; a manifest change makes the run `full` anyway); type checks and tests on the
   language's types. `always_run` is for cheap
   hooks only. A tool that can scope itself reads `PRE_COMMIT_FROM_REF` and runs complete when it
   is unset: `vitest run ${PRE_COMMIT_FROM_REF:+--changed "$PRE_COMMIT_FROM_REF"}`, or
   `TURBO_SCM_BASE=$PRE_COMMIT_FROM_REF turbo run test --affected`. Go needs nothing extra: its
   test cache already reruns only the packages a change affects, and a cached run still writes a
   complete coverage profile.

Scoped runs trade completeness for speed. A partial TypeScript run cannot enforce a
whole-project coverage floor, and import graphs miss hidden dependencies (fixtures, generated
files, environment). CI runs every check completely, with cache-defeating flags such as
`-count=1 -shuffle=on`, before merge.

`gate check --fast` runs the guards and the `pre-commit` stage in place, on local staged,
unstaged and untracked changes, and writes no record. Agent hooks use it for quick feedback, and `ship` never
accepts it. It prints the stage duration, and adds a `fast-tier-slow` note when that exceeds 5 s.
The full `gate check` runs both stages on the exact commit, scoped as in §4.2.

## 5. Review

`gate review` requires an effective passing check on the exact SHA, current merge-base and lane.
Docs changes do not require a review.

### Packet

The packet is built by Gate. It contains:

- The diff from the merge-base, or from the last reviewed SHA when it is an ancestor of the
  candidate, with surrounding context. Delta selection does not depend on the open-finding count.
- The intent: commit messages in the range, plus `--intent FILE` if given.
- For a delta review, open findings with their IDs, and prior accepted/resolved findings with
  their dispositions and recorded reasons. Unchanged accepted findings should not be re-raised;
  a genuine recurrence after resolution can be a new finding.

The packet never contains check results or author reasoning, and it is size-bounded.

### Reviewer

One fresh reviewer explicitly covers correctness, security and simplicity. Concrete security
violations use the correctness-plus-repro blocking contract; simplicity stays advisory unless it
identifies a concrete correctness failure. A reviewer-written repro is a claim, not an executed
reproduction.

The reviewer runs as a subscription CLI subprocess through the shared bounded runner.

- Its working directory is an empty temporary directory and the packet goes on stdin.
- The environment is built from an allowlist: `HOME`, `PATH`, `LANG`, `LC_ALL`, `USER`, a private
  `TMPDIR` and the CLI's own config directory. API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CODEX_API_KEY`, …) are
  removed, so a billed fallback cannot happen.
- No tools and no persisted session.

Reviewer commands:

- **claude:**

  ```
  claude -p --output-format json --json-schema <schema> --model <m> --tools "" --setting-sources "" --strict-mcp-config --disable-slash-commands --no-session-persistence --system-prompt <prompt>
  ```

- **codex:**

  ```
  codex exec -C <empty> --skip-git-repo-check -s read-only --ephemeral --ignore-user-config --ignore-rules -m <m> --output-schema <file> --json -o <file> -
  ```

  Any tool or command event in the output stream invalidates the result.

`GATE_REVIEWER` selects the reviewer; `--reviewer` overrides it. With `auto`, Gate picks the
family other than the author's. The author's family is read from `GATE_AUTHOR` (`claude` or
`codex`) or the Pi integration, and defaults to the codex reviewer. Reviewer choice is a property
of the person running Gate, not of the repository, so it is never read from `gate.yaml`.

### Response

The response follows one fixed JSON schema:

```json
{"findings":[{"kind":"correctness|contract|data|test-quality|simplicity|adjacent",
  "path":"…","line":12,"claim":"…","repro":{"input":"…","expected":"…"}}],
 "resolved":["F1"]}
```

Gate validates the response strictly. It never repairs one, and it retries at most once on
invalid output.

- A `correctness` finding with a repro is blocking. Every other finding, and a correctness
  finding without a repro, is a note.
- New findings get the next `F<n>` ID.
- `resolved` may only name open IDs. Those IDs are marked resolved.

### Budget

Each branch gets 2 logical reviews: the initial review and one changed-commit review, using a
delta when ancestry permits. Each logical review may retry invalid output once; both subprocess
attempts share that logical slot. Attempts are persisted before invocation and counted separately.
Admission, invocation and publication are serialized per branch to prevent budget oversubscription.

A valid recorded review for the exact SHA, merge-base and branch is reused without invoking a
reviewer, spending budget or rewriting its raw result. Current decisions determine its effective
findings. The final permitted clean review can pass. A further required review parks the branch;
its open blocking findings then require `gate decide accept`, and `gate decide approve` is required
for the current SHA before shipping.

A rate-limit or auth failure on the reviewer CLI is a blocked result (exit 3). A no-output
unavailable call spends no logical slot, but its invocation still counts as an attempt. Invalid
output spends a logical slot, whether or not its allowed retry succeeds.

The effective review result for a SHA is `pass` when no blocking finding remains open. This alone
is not shipping eligibility: all other requirements, including a parked-branch decision, still apply.

## 6. Verify

`gate verify` is required when a changed path matches `verify.surfaces`. Otherwise its verdict
is `no-surface`.

In a detached worktree of the SHA, Gate runs `verify.setup`, then each scenario. A scenario
compares the exit code, and optionally `stdout_contains` or `stdout_equals`, with optional stdin.
An explicit empty stdout assertion differs from an absent assertion. Expected nonzero exits are
valid, but a process startup failure is not an observed exit. Truncated capture is inconclusive
only when the assertion cannot be decided; a provable mismatch remains a failure.

| Verdict | When |
|---|---|
| `go` | Every scenario holds. |
| `no-go` | An assertion failed. This blocks. |
| `inconclusive` | Setup failed, a scenario could not complete, or bounded output left an assertion undecidable. This blocks until fixed or precisely waived. |

Assertion failures take precedence over uncertainty: a mixed run is `no-go`, not waivable
`inconclusive`. `gate decide waive verify` covers only a current, coherent `inconclusive` record
for the exact SHA and merge-base. It cannot waive missing evidence, `no-go`, or tracked-content
mutation. Decisions never rewrite the recorded verdict.

## 7. Ship

`gate ship` refuses (exit 3) unless all of the following hold:

- HEAD equals the SHA, and the working tree is clean.
- The lane's required steps have fresh, coherent evidence for the SHA and current merge-base,
  passing under the same decision-aware assessment used by `status`.
- No blocking finding is open.
- The branch is not parked, or a decision covers it.
- A Sign-off lane has an `approve` decision for that SHA.

Gate then:

1. Resolves the selected remote's single push destination (default remote: `origin`), reads its
   branch tip, and pushes the exact SHA to that same destination with an explicit force-with-lease.
   Multiple push destinations or chained URL rewrites that could change the target fail closed.
2. Opens or updates the PR with `gh` targeting that GitHub repository and host, with a body that
   summarises executed steps, effective findings and notes.
3. Sets `gate/verified=success` on that SHA in the same repository through `gh api`.

Local evidence and reviewer claims are distinguished in the PR body. Gate does not claim remote
CI passed, merge approval was granted, or rollback readiness was established. Shipping uses the
caller's gh authentication; the selected repository must be writable by that identity.

`--dry-run` prints the plan without running it. Setting up branch protection is documented, not
automated.

## 8. What blocks

Only these block a commit. Everything else is a note in `status` and the PR body.

- Failing repository checks or missing tools.
- Guard hits.
- A missing REQUIRED setup obligation.
- An open blocking review finding.
- A verify `no-go` or `inconclusive` verdict.
- A missing Sign-off approval.
- A further required review beyond the logical review budget, without the required human decisions.

## 9. Agent integration

`gate agent-hook` reads a tool-call JSON on stdin. It accepts either Pi's `{toolName,input}` or
Claude Code's `{tool_name,tool_input}`.

It exits 2 with a reason when the call would:

- Bypass hooks: `--no-verify`, `-n` on commit, `core.hooksPath`, or `SKIP=`/`PREK_SKIP`.
- Edit hook or Gate config.

This is noise reduction, not enforcement. `ship` re-checks everything.

`integrations/pi/gate.ts` is the Pi extension. Its hooks are:

- `tool_call` calls `gate agent-hook`.
- `agent_end` runs `gate check --fast`.

Claude Code hook examples are documented.

## 10. Build order

1. Kernel: state, config, lanes, check with prek, status.
2. Guards and setup audit.
3. Ship and decide. This is a working gate with no AI.
4. Review through subscription CLIs.
5. Verify scenarios, then the agent hook and Pi extension.
6. Dogfood on Gate itself and on a TypeScript repository; measure; then consider the revert test
   and mutation testing in Sign-off.
