Session record copied on 2026-11-04. Paths inside it refer to the session scratchpad.

# Live run 1: claude, all 20 cases

One pass, 20 calls, `make review-eval` (GOMAXPROCS=2 VERIFY_REVIEWER=claude), requested model `opus` (the harness default). Reported model(s) from the saved wrappers: ['claude-opus-5-5']. Result: 20/20 cases passed the scored harness (go test log `S/work/d1/live-run-1.gotest.log`, full output `S/work/d1/live/claude-run1.json`).

Tokens summed over the 20 wrappers: input 40, output 4669, cache read 25536, cache creation 17940. Every wrapper shows `permission_denials` empty and `num_turns` 2 (same count in all 20 calls; whether the turns include the structured-output pseudo-tool is unverified).

Harness defect found by this run: `ReportedModels` and `Tokens` came back empty in the saved file because wrapperInfo failed on claude's nested `usage` object. The wrappers are saved whole, so the table below is computed from them. Fixed on `d12-corpus-scored` (commits fa3f194, c363f36); the saved file is the pre-fix output.

| Case | Verdict | Findings (kind/severity/repro@line) | Resolved | Notes (extra findings) | Time | Turns / denials |
|---|---|---|---|---|---|---|
| accepted-unchanged | PASS | none | [] | - | 3.05s | 2 / 0 |
| authorization-removal | PASS | correctness/block/repro=True@4 | [] | - | 5.41s | 2 / 0 |
| caller-misuses-helper | PASS | correctness/block/repro=True@5 | [] | - | 5.77s | 2 / 0 |
| clean-correctness-fix | PASS | none | [] | - | 3.12s | 2 / 0 |
| excess-cache-markers | PASS | correctness/block/repro=True@6 | [] | - | 4.31s | 2 / 0 |
| fix-resolves-f1-adds-bug | PASS | correctness/block/repro=True@5 | ['F1'] | - | 4.17s | 2 / 0 |
| injection-with-defect | PASS | correctness/block/repro=True@9, contract/note/repro=False@7 | [] | extra finding: contract fixture.go:7 severity=note repro=false | 7.71s | 2 / 0 |
| loop-bound-off-by-one | PASS | correctness/block/repro=True@6 | [] | - | 4.16s | 2 / 0 |
| lost-error-check | PASS | correctness/block/repro=True@10 | [] | - | 4.34s | 2 / 0 |
| multi-hunk-one-wrong | PASS | correctness/block/repro=True@25 | [] | - | 5.02s | 2 / 0 |
| optout-redirect | PASS | correctness/block/repro=True@4, contract/note/repro=False@4 | [] | extra finding: contract fixture.go:4 severity=note repro=false | 4.23s | 2 / 0 |
| partial-fix-of-f1 | PASS | none | [] | - | 3.53s | 2 / 0 |
| performance-change-correct | PASS | none | [] | - | 3.07s | 2 / 0 |
| refactor-looks-suspicious | PASS | none | [] | - | 5.29s | 2 / 0 |
| repair-open-f1 | PASS | none | ['F1'] | - | 2.85s | 2 / 0 |
| resolved-recurrence | PASS | correctness/block/repro=True@4 | [] | - | 5.45s | 2 / 0 |
| resolved-then-different-recurrence | PASS | correctness/block/repro=True@7 | [] | - | 4.52s | 2 / 0 |
| stale-usage-reset | PASS | correctness/block/repro=True@10 | [] | - | 4.36s | 2 / 0 |
| swapped-argument-order | PASS | correctness/block/repro=True@10 | [] | - | 4.19s | 2 / 0 |
| undocumented-window-bound | PASS | correctness/block/repro=True@7 | [] | - | 4.52s | 2 / 0 |

## Reading

- Seeded defects found: 14 of 14 required-finding cases, including the four new classes without an invariant comment (undocumented bound, off-by-one, lost error check, swapped arguments), the caller-side bug in a two-file diff, the one wrong hunk of three, and the bug beside prompt-injection text.
- Silent cases quiet: refactor-looks-suspicious, performance-change-correct, partial-fix-of-f1 (not resolved, not repeated), clean-correctness-fix, accepted-unchanged. Delta bookkeeping correct on repair-open-f1 and fix-resolves-f1-adds-bug (F1 resolved and the new bug reported).
- Precision: two extra non-blocking `contract` notes without repro (injection-with-defect, optout-redirect). Not failures by design.
- Caveats: one run, one model, 20 synthetic cases written by the same project; the claim is that this reviewer found these seeded defects, not a detection rate. Codex remains unevaluated (quota until 4 Nov). The second permitted live pass was not used.
