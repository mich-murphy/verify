# Status

Verify v1 checks each commit on the exact SHA with the repository's own prek hooks plus its own guards. It picks a lane (docs, standard or signoff) from the diff. A blocking guard skips the hooks, and a passing check on an identical tree is reused. One fresh reviewer (claude or codex, opposite the author) reviews the change. Changed CLI surfaces are exercised. `verify ship` pushes only the checked commit and sets `verify/verified`. It works on macOS and Linux with GitHub. CI on both operating systems passed on main at 9fbd2a8 (run 41733971895); the parent runs the same `make ci` gate on this docs branch before it merges.

## Merged units

Evidence for every row is in `docs/evidence/`, session records copied on 2026-11-04. The plan numbers each unit.

| Unit | PR | What it does |
|---|---|---|
| Rename | #1 | Gate becomes Verify. |
| Hardening | #2 | Execution lifecycle hardened and battle-tested (docs/battle-testing.md). |
| 1 Docs lane | #4 | Docs lane takes no executables, and docs-only diffs are exempt from the 400-line rule. |
| 2 Reviewer selection | #5 | Opposite the author, claude by default, one fallback recorded. |
| 3 Status | #6 | Exit codes, waived and accepted display, approve coverage. |
| 4 Guard short-circuit | #7 | A blocking guard skips the hooks. |
| 8 Audit notes | #8 | Notes for scope, always-run, uncacheable and missing-tool hooks. |
| 9 Onboarding | #9 | One blocking finding, printed YAML, hook-manager note, no uncached test hook. |
| 10 Fixture scope | #10 | Fixture-only changes run full scope. |
| 11 Dry-run plan | #11 | `ship --dry-run` prints the plan before resolving the remote. |
| 0 Hook env | #12 | Test recipes scrub `GIT_DIR`, so hooks are safe from linked worktrees. |
| 5 Hook findings | #13 | One finding per failed hook (`check:<stage>:<hook>`), skipped steps, per-attempt logs, fixer rewrites. |
| 6 Check reuse | #14 | Identical tree reuses a passing record, guards first, 24-hour bound. |
| 7 Lock wait | #15 | Lock wait with a progress line and cancellation. |
| 12 Scored corpus | #16, #17 | Structured expectations, abort on unavailable, `make review-eval`, 20 cases. |
| 13 Property tests | #18 | rapid tests for eligible, lane, check scope and review parse. |
| 13 Mutation | #19 | `make mutate` runs gremlins over the decision packages. |
| 13 Test hygiene | #20 | Flaky concurrent review test fixed, HOME and TMPDIR isolated, Pi harness in CI. |
| 13 End to end | #21 | Subprocess tests on Go and TypeScript fixture repositories. |
| Hygiene | #23 | testenv git variables, uncached regex, coverage.md lane, rapid `.fail` ignore. (#22 closed, superseded.) |
| Setup fixes | #24 | Hook scope judged by real project files; refused targets shown in the preview. |

## Measured

| Claim | Before | After | Evidence |
|---|---|---|---|
| Guard short-circuit, pi-extensions | 99.11 s | 0.33 s | `docs/evidence/replay.md` (a)1, e3a |
| Guard short-circuit, memory | 351.83 s | 0.36 s | `docs/evidence/replay.md` (a)1, m3 |
| Docs lane, 452-line markdown file on memory | signoff | docs in 1.6 s | `docs/evidence/acceptance.md`, notable live results |
| Check reuse, pi-extensions warm check | 97.79 s | 0.25 s | `docs/evidence/replay.md` (a)4, e0 |
| Check reuse, memory warm check | 365.37 s | 0.36 s | `docs/evidence/replay.md` (a)4, m6 |
| Fixture-only change | false green in 0.55 s | caught, check fails in 427.87 s | `docs/evidence/replay.md` (a)9, m7 |
| Onboarding on memory | 11 findings, 9 blocking | 1 blocking plus a hook-manager note | `docs/evidence/replay.md` (a)7, m0 |
| Total coverage | 85.5 % (previous STATUS.md at 9fbd2a8) | 86.6 % | `docs/evidence/acceptance.md`, stack C and d18; floors stay 80 % total, 70 % per package |
| Mutation, lane | not run | 10 killed, 0 lived, 6 not covered (100 % efficacy, 62.5 % of all) | `docs/battle-testing.md`, Mutation |
| Reviewer corpus, live claude | unevaluated | 20 of 20 cases pass | `docs/evidence/reviewer-live-run.md`, PR #17 |
| Replay of 34 experiments | not applicable | 11 intended differences, 0 regressions | `docs/evidence/replay.md` |

Notes on the table.

- Wall times are single runs on one machine. Same-work runs elsewhere varied up to 20 % (`docs/evidence/replay.md` (c)1).
- The mutation score is 100 % on efficacy for all five packages. The share of all mutants killed is below the 85 % target for review (81.3 %), lane and guard (62.5 % each), because uncovered mutants are the gap.
- The reviewer result is one run, one model (`claude-opus-5-5`) and 20 synthetic cases written by this project. It shows this reviewer found these seeded defects. It is not a detection rate.

## Not done

- The sessioncapture and syncer tests in memory still miss the 20 s budget. In the replay `pre-push took 410.9s, over the 20s budget`. This is memory work.
- The three dogfood PRs (pi-extensions, memory, businesscraft) are landed by each repo's owner. Verify's findings go back to the authoring agent.
- The codex corpus run waited on quota until 4 Nov. It has not run.
- A queued check does not re-read the record after it gets the lock, so it reruns every hook on a SHA the holder just passed (`docs/evidence/replay.md`, open gap). Fix in review, PR #25.
- The `setup:test` finding says `verify setup --apply` writes the hook while apply refuses the only hook that could satisfy it (`docs/evidence/replay.md`, observations). Fix in review, PR #25.
- The tag v1.0.0 is created by the owner.

## Known limits

- Verify is cooperative. It is not a hostile-code sandbox, an autonomous repair agent or a CI, merge or deploy service.
- The e2e package skips locally when `prek` is missing and fails closed when `CI` is set. Its TypeScript tests skip on node older than 22.18.
- Remote CI is the approval for a merge. Local results are not.
- Live reviewer accuracy beyond the seeded corpus, real shipping and a cold filesystem cache are not qualified (`docs/battle-testing.md`).
- On a docs-lane change, always-run `typecheck` and `test` hooks still run, so pi-extensions takes 92.03 s until its dogfood PR lands.
- Branch protection and integration activation are explicit user steps.
