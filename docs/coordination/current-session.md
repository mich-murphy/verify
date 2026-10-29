# Gate → Verify rename

## Checkout and scope

- Active public repository: `mich-murphy/verify`, task base
  `5969353d0b808228eccad0fed8911ee7ea145032`; initially clean.
- The preserved private Gate checkout remains unchanged.
- User requested a project rename: product Verify, executable `verify`, config `verify.yaml`,
  `VERIFY_*` environment, `verify.*` version-1 records, git-common-dir `verify/` state and
  `verify/verified` status. Module/remote already use `github.com/mich-murphy/verify`.
- Integrations, examples, build commands and spec terminology updated without policy changes.
  No aliases or automatic state migration; README documents the manual transition.
- User subsequently authorized commit and push. Delivery uses the `rename-gate-to-verify`
  branch in a single-layer `gh stack` based on `main`, with a review PR; no merge.
- No history rewrite, global install, user config or GitHub protection changes.

## Ownership and decisions

- Parent owns acceptance and all files. Worker `f0ee8233-0751-4bf` completed implementation,
  tests and integrations and explicitly released ownership. No active workers.
- `verify verify` preserves the scenario subcommand; no unrelated command redesign.
- Internal test helper `runCLI` avoids collision with the existing `internal/verify` import.
- Formatting inventory now includes new untracked Go files and skips missing tracked paths,
  allowing checks before staging renames. A regression covers renamed/new/deleted/ignored files.

## Verification and acceptance

Accepted locally against task-base plus the uncommitted rename:

- Worker: `go test -race -count=1 ./internal/... ./cmd/verify/...` passed all 18 packages;
  `go test -race -count=1 ./cmd/verify` passed after the inventory regression.
- Worker: installed-Pi `node --test integrations/pi/verify.test.cjs` with `PI_PACKAGE` set
  passed one test with no skips; scoped golangci-lint reported zero issues.
- Parent: `make check`, `make fmt-check`, `make build` and `git diff --check` passed.
  Coverage 85.5%, unchanged 80% total / 70% package floors.
- Parent: built binary root help, all nine JSON help commands, structured invalid-flag output
  and agent-hook blocks for config writes/hook bypass passed.
- Parent compared 55 production/integration files with the expected terminology-only transform;
  all matched. SPEC comparison also confirmed terminology-only changes. Remaining old-name
  references are migration guidance, negative fixtures or unrelated ordinary words.
- Full gate log: `/tmp/verify-rename-make-check.log`. No live reviewer or remote-CI run.

## Risk and recovery

Old executable/config/environment/status names no longer work with Verify. Existing Gate state
is retained, not imported as Verify evidence. Update config, agent activation and required
GitHub status explicitly, then regenerate evidence without evading prior budgets or findings.
Settle interrupted Gate processes before switching; never delete a quarantine marker blindly.
Revert the rename commit to return to Gate; no existing state was modified or removed.

## Next action

Commit and push the accepted rename, open its review PR, and watch the actual GitHub CI run.
Global installation, integration activation, merging and branch-protection updates remain
explicit follow-up actions.
