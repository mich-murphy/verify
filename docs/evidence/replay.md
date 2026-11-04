Session record copied on 2026-11-04. Paths inside it refer to the session scratchpad.

# Replay summary (plan unit 14), saved by the parent from the replay agent's hand-back

Binary: origin/main 69e4e0a7199d67851653b99e3529c5e8a0b7aaca (verify-sha.txt). Baseline binary: 51640bb. Per-experiment diff tables: diff/<repo>-<exp>.md (35 files, diff.sh). Raw logs: exp/, exp-mem/, exp-bc/. One line per experiment: progress.md.

Counts. Replayed 34 of 36 baseline experiment directories (pi 14/15, mem 9/10, bc 11/11). Skipped 2 (the two `*.invalid*` baseline runs). Skipped sub-steps: mem M1's first two checks (baseline machine state: no golangci-lint, Go 1.26 panic), bc 1-docs/stale-index and 7-squash/attempt1-main-only, pi branches exp-e4c, exp-e9b, exp-fixture (no baseline). Extra replays with no pair: bc 1-docs-eof-autofix, mem 1-onboard/setup-no-golangci. Differences: (a) intended 11, (b) regression 0, (c) environment 5, plus one open gap. Reviewer calls: 5 through VERIFY_REVIEWER=claude (pi e2b, e6, e8 delta; mem m2; bc b2); three refused without a model call; codex unused.

Setup differences from the baseline: origin/main and origin/HEAD set to the baseline clones' values; .git/verify removed before every experiment except the chained ones (pi e0 cold then warm, e2b then e8, e5, e6, e9; mem m6; bc 1-docs and 2-abn), which also removes the worktree's node_modules (cause of most bc wall differences); mem M1 replayed with the new onboarder on the 2deac62 tree; golangci-lint v2.15.0 from S/toolbin for mem; the parent's HOLD pauses are logged in progress.md.

## (a) Intended by the plan (11)
1. Unit 4 guard short-circuit, check wall: pi e3a 99.11 s to 0.33 s; e3b 98.67 to 0.35; e4a 103.43 to 0.39; e5 100.58 to 1.05; mem m3 351.83 to 0.36; bc b3 1.70 to 0.70; b4 2.09 to 0.75; b5 1.72 to 0.55; b7 33.63 to 2.73. Steps `skipped`, message "guards blocked; hooks skipped"; the extra check:prek-pre-commit findings beside the guard are gone; guard rule ids identical.
2. Unit 5 hook findings: pi e2 check:prek-pre-push became check:pre-push:test; mem m4 check:pre-push:tidy; bc b9 check:pre-commit:actionlint with the `ubuntu-lates` message, and b9 moved from exit 3 lane null to exit 1 lane signoff with six hook findings; bc 1-docs-eof-autofix "modified docs/devops/dev-staging-cicd-manual-steps.md; run the fixer and commit", exit 1 lane docs (baseline: blocked, exit 3, no lane, empty reason).
3. Unit 5 skipped steps: pi e7 and bc b8 list prek-pre-commit and prek-pre-push as skipped after a failed prepare-deps.
4. Unit 6 reuse: pi e0 check-warm 97.79 s to 0.25 s; mem m6 check-warm 365.37 s to 0.36 s; message "check already passed for this commit"; reused_from null for the same SHA (SPEC line 128).
5. Unit 3 status: pi e0 status 0 to 3; pi e2 status-after-check 0 to 1; mem m0 status 0 to 1; pi e5 accepted finding state `accepted`, status ok exit 3.
6. Units 8 and 5 notes: setup:typecheck:always-run, setup:test:always-run, setup:lint:scope (pi e6), setup:coverage:uncacheable (mem), setup:ci-oxfmt:scope and setup:ci-oxlint:scope (bc, false positives fixed by PR #24); budget notes such as mem m2 "pre-push took 410.9s, over the 20s budget".
7. Unit 9 onboarding: mem m0 11 findings (9 blocking) became 1 blocking (setup:prek-config) plus the setup:hook-manager note; mem m1 setup --apply no longer writes the uncached `make cover` hook and names a cacheable target; apply exit 0 to 1; the following check 366.83 s pass to 14.79 s fail on setup:test and setup:coverage.
8. Unit 1 docs lane: mem m5 (452-line docs file) signoff to docs, 0.51 s to 0.93 s.
9. Unit 10 fixture scope: mem m7 pass 0.55 s to fail 427.87 s with "scope: full (fixture-only change)"; check:pre-push:coverage fails on recovery_test.go:1444 "archive record: noncanonical descriptor", the baseline's documented false green now caught (exp-mem/7-longpath/prek-pre-push.log).
10. Unit 7 lock wait: pi e9 check-B stderr now "waiting for check.lock held by pid 76143".
11. Unit 11 dry-run plan: pi e6 ship --dry-run lists push, PR and status before "remote failed: selected remote is not a GitHub repository".

## (b) Regressions: 0
Lanes, guard ids, review outcomes and decide outputs match the baseline apart from (a). Reviews: pi e2b, e6 and bc b2 correctness block; pi e8 delta resolves F1; pi e8 third review refused; bc b6 review refused.

## (c) Environment (5)
1. Wall-time noise: pi e8 review-delta 5.88 s to 3.10 s; pi e2b direct vitest 8.83 to 12.18; bc b9 30.03 to 40.55; mem m2 check 351.38 to 420.82 (+19.8 %); pi e6 check 97.66 to 104.34. Other same-work runs within 20 %.
2. bc cold-worktree walls (fresh install of 1432 packages after each state wipe): b1 check1 1.40 s to 10.74 s; b2 2.67 to 9.83; b4n 1.60 to 12.91 and 1.95 to 9.88; b8 1.00 to 5.08. Not a Verify change.
3. First-install prepare-deps failure "command exited before owned descendants settled" (baseline side finding A) hit bc empty-diff check1, b4n check-negative2 and b6; a warm-worktree rerun of those three was denied by the permission layer and not retried. Manual: keep S/replay/bc/.git/verify, `cd S/replay/bc; git checkout -q -f exp-b6; S/replay/rb.sh 6-narrow check S/replay/bin/verify check --json`.
4. Flaky repo tests: mem m6 cold and onefile failed on sessioncapture timing in the baseline, passed in the replay (380.27 s, 352.11 s).
5. Reviewer nondeterminism: mem m2 review one `contract` note in the baseline, 0 findings in the replay.

## Open gap (baseline behaved the same)
pi e9: check-B waited for A's lock (189.17 s total, A took 95.31 s) then re-ran every hook on the same SHA A had just passed; reused_from null. SPEC line 127 says a passed record for the same commit is returned as recorded; the queued process does not re-read the record after acquiring the lock. Evidence: exp/09-concurrency/{check-A,check-B}.json, log.txt.

## Observations with no baseline pair
- pi e1: the docs lane still takes 92.03 s because the always_run typecheck and test hooks still run (addressed by the pi-extensions dogfood PR).
- mem m1: the setup:test finding still says "`verify setup --apply` writes it" while apply refuses the only hook that could satisfy it; only setup:coverage carries the refusal text.
- bc b7: guard:secrets:web/agent/scripts/eval-ask-ptc.test.ts:366 is a generic-credential-uri hit on a fake fixture URL; guard code unchanged between 51640bb and 69e4e0a; previously hidden by the auto-fixer block.

Untouched: S/pi, S/mem, S/bc and the reference repos; bc main and origin/main restored to 8ab995dc54; nothing pushed.
