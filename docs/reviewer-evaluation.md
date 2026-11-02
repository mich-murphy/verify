# Reviewer evaluation

`testdata/review/` holds synthetic Go cases, each a `before.go`, `after.go`, `oracle_test.go` and a JSON manifest. They are newly authored illustrations, not extracted repository changes. No external source or history is implied.

## Manifest

- `Expect` is the scored answer, parsed once when the corpus loads. A case that needs a finding has `path`, `kind`, `blocking` and `repro`. A silent case has `"findings": "none"`. Every case has `resolved`, the exact set of open IDs the diff fixes (`[]` when none may be resolved).
- `Why` is free text for humans and is never sent to a reviewer.
- `Extra` lists further changed files, stored as `before.<name>` and `after.<name>`, for multi-file diffs.
- The hashes, the oracle verdicts before and after, and the review `Input` complete the manifest.

## Cases

The first eight are one-line deletions with a doc comment that states the broken invariant, the easiest form of review. The other twelve cover what those lack: a bug with no invariant comment, a multi-hunk file where one hunk is wrong, a two-file diff where the caller is wrong, a loop-bound off-by-one, a lost error check, swapped same-typed arguments, a correct refactor and a correct performance change (both silent, measuring false positives), instructions to the reviewer inside the diff, a partial fix of F1 that must not be resolved, a fix of F1 that adds a new bug, and a different bug in a function with a resolved finding.

## Integrity (runs in ordinary tests)

`TestReviewCorpusIntegrity` verifies every hash, replays each diff against the exact before bytes, runs the oracle against both versions, and checks the packet stays under 8 KiB and contains none of `Why`, the encoded `Expect`, the hashes or oracle names. It proves the cases are well formed. It says nothing about any reviewer.

## Live run

`make review-eval` calls the `claude` subscription CLI once per case and writes `.verify-eval/claude-<timestamp>.json` (gitignored). Calls go only through subscription CLIs, never a billed API, so this never runs in CI. Set `VERIFY_CLAUDE_MODEL` to choose a model (default `opus`). For codex, run the same `go test` command by hand with `-review-eval=codex`.

The run aborts on the first `ErrUnavailable` (missing CLI, login, quota), so an outage costs one attempt. `-review-eval-case=ID` runs a single case. The output path must not exist.

## Scoring

Per case the test fails unless:

- a required finding exists with that path, kind, blocking severity and a non-null repro;
- a silent case has zero findings;
- `resolved` equals the expected set exactly.

Extra findings on a bug case are not failures. They appear in `Notes` so precision is visible without failing recall.

## Reading the output

The file is a JSON array with one object per call:

- `Pass`, `Failures`, `Notes`: the verdict, why it failed, and extra findings.
- `Parsed`: the validated response. `Raw`: the answer as returned. `Error`: an unavailable reviewer or invalid output.
- `Wrapper`: the CLI's full stdout (claude's result object, codex's JSONL events). Check `permission_denials` and `num_turns` there to audit tool use.
- `ReportedModels` and `Tokens` (input, output, cache read, cache creation): taken from the wrapper, not from the requested model.
- `Duration`, `PromptSHA256`, `PacketSHA256`.

Keep these files private. A passing live run on these cases supports the claim that this reviewer found these seeded defects, not that Verify catches defects in general.
