# Simplification and battle testing — accepted locally

## Checkout and authority

- Active checkout `/Users/mm/dev/verify`, branch `rename-gate-to-verify`, starting and current HEAD
  `ee4777d4286193416d80b2c145d4e9b3505ba224`. Starting tree was clean; task changes are uncommitted.
- Private `/Users/mm/dev/gate` preserved. No commits, pushes, history changes, global installation,
  live model calls, real shipping, reference-repository edits or SPEC changes.
- User requested justified structures/simplicity, adversarial core testing and measured dependable
  performance; all workers used `openai/gpt-6.1-sol`, medium effort. Parent owns acceptance.

## Ownership

All workers completed and released ownership; parent now owns all changed files.

- `bad69842-ac25-41a`: state/evidence/review/ship inventory, identity/projection and gh lifecycle fixes.
- `8d5b9d3f-32f6-458`: execution/path/guard inventory, Git lifecycle migration and worktree quarantine.
- `a9a40eba-be4e-49c`: config/audit/onboarding/CLI inventory, quoting/flags, CI duplicate removal.
- `41507980-8764-486`: independent read-only correctness review, including parent correction follow-up.
- `0e05d931-b111-41d`: measured runner/gitx performance correction.
- `4897ee78-eab8-494`: independent read-only performance/safety review.
- `5eeea843-ae53-4cc`: serialized final qualification, Linux environment, durable report and STATUS.

Expired worker sessions were replaced only after ownership release; retained reports supplied context.
Steering unexpectedly aborted some early worker turns; explicit resumes reconciled corrections.

## Accepted changes and corrections

- Inventory reconciled 17 internal packages, 51 production Go files and 84 named types. Ordered
  slices, transient identity maps, strict raw records, provenance, locking and quarantine remain
  justified. No new dependency/framework/cache/database/schema; no arbitrary LOC target.
- Fixes: immutable finding identity versus disposition; effective PR findings; Git quoted/binary/mode
  and trailing-whitespace paths; unique deleted-test IDs; precise truncated contains assertion;
  audit flag boundaries; safe onboarding arguments; supported YML hook protection.
- Parent caught an intermediate FileAt regression: nonregular policy must not become absent.
  ErrNonRegularFile now propagates for config; only generated-header inspection intentionally skips it.
  Actual trusted/candidate policy regressions and generated regular→symlink protection pass.
- Real fake-process experiments proved gh/local Git/remote Git descendant-pipe hangs and unbounded
  remote capture. Shared runner now handles bounded output, cancellation, exit distinctions and
  settlement. Sticky Git quarantine blocks later execution/destructive fallback; check/verify consult
  settlement at marker release. Parent caught gh overflow falling through to PR creation; unusable
  view output now stops orchestration, with invocation-log regressions.
- Simplification removes duplicate subprocess/capture paths, duplicate CI race-test execution and
  unused CLI arguments. CI retains race/shuffle/uncached coverage, unchanged 80/70 floors and cached
  local hooks. Direct FileAt reads eliminate redundant resolution/metadata subprocesses.

## Measured performance acceptance

Initial hardening regressed controlled CLI overhead by >2×; parent rejected it. Focused correction
skips process-table scans only when the OS probe returns ESRCH; all other results preserve bounded
inspection, zombie handling and error/quarantine behavior. No lifecycle check was removed.

Same-window median warm check seconds (3 samples; baseline / hardened-before-optimization / final):
- 1 changed file: .5306 / 1.3177 / .4278.
- 10 changed files: .9019 / 2.3970 / .6598.
- 100 changed files: 4.6818 / 13.3119 / 2.9347.

Real Git, controlled fake prek/probe, fresh/reused disposable worktrees; not real tool/model/network
latency or cold OS caches. Independent final 100-file rerun: 2.9565s median. Final binary is byte-
identical to the measured fixed binary. No universal latency guarantee inferred.

## Final evidence and acceptance

Parent inspected production diffs, new regression contracts, independent reviews, measurements and
final gate results. Worker completion was not acceptance; the corrections above were inspected and
retested. Parent accepts this scoped local simplification/hardening pass, not exhaustive correctness.

Final-source worker verification (serialized, bounded, source manifests stable):
- `make check` passed before and after report updates; final post-doc run 7.37s.
- `go test -race -shuffle=on -count=1 -timeout=180s ./...`: all 18 packages passed on macOS arm64.
- Five focused race repeats include new FileAt/process-group/probe/cleanup tests; passed.
- Installed-Pi harness: 1 pass, 0 skips, no model call. Coverage 86.4%, unchanged 80/70 floors.
- Four bounded fuzz campaigns and eleven core benchmark packages; runner/gitx refreshed after
  performance correction, unaffected evidence reconciled against unchanged source.
- Offline Linux arm64: all 18 test binaries passed, CGO_ENABLED=0/no race, no exclusions. Initial
  attempt lacked Go for corpus oracles; corrected using checksum-verified official Go 1.27.3 mounted
  read-only. Existing image only, no pulls/container network/credential mounts or global install.
- Parent independently reran `make check` and `git diff --check` after accepting the report;
  both passed. Log: `/tmp/verify-battle-review/parent-final-make-check.log`.

Durable report: `docs/battle-testing.md`; short update: `STATUS.md`.
Detailed logs, before/after failures, source/binary hashes and fixtures:
`/tmp/verify-battle-review/` (especially `qualification/phase2-*` and `performance-fix/`).
Temporary artifacts are local evidence, not permanent public links.

## Limits and next action

No live reviewer accuracy, real shipping, remote CI, real dependency-install workload, Linux race,
other CPU architectures or cold filesystem-cache claim. Remaining source-review hypotheses are
listed in the durable report, not represented as executed failures or resolved guarantees.
No further implementation work assigned. User approved plain publication: one cohesive child commit
on `simplify-and-battle-test`, stacked over the still-open rename PR #1; no merge or Verify ship.
Publication worker owns this brief only; source remains frozen to the accepted qualification manifest.
Publication preflight `make check` and `git diff --check` passed; accepted source hashes still match.
Normal commit/push hooks remain enabled. Remote CI is pending publication, not yet approved. On interrupted ownership, inspect/stop survivors before removing a marker;
never clear quarantine merely to retry. Reverting lifecycle work requires coordinated runner/gitx/
check/verify changes; the small performance correction is independently reversible.
