# Maintainer handoff

The initial public snapshot keeps the Gate v1 cooperative CLI design. Its Go module is
`github.com/mich-murphy/verify`; its executable is `gate`. The license is MIT.

## Accepted scope

- Exact-commit checks, semantic evidence validation, decision-aware admission and safe worktree reuse.
- Shared bounded execution, convergent review, precise verification and consistent GitHub targeting.
- Original synthetic audit/reviewer fixtures; no external application source or development archive.
- Unchanged quality thresholds and explicit opt-in model evaluation, never model calls in normal CI.

The synthetic corpus validates source hashes, exact patch replay and before/after Go oracle
assertions. Failed integrity validation stops before live evaluation. Its contents are not a
historical benchmark and have no inherited live-model scores.

## Verification and limits

Release checks include `make ci`, `make check`, the installed-Pi harness, a failing-integrity
probe with a fake reviewer, and public-content/secret review. See [STATUS](../../STATUS.md) for
results and limitations. GitHub's actual workflow result is authoritative for remote CI.

No worker owns files after handoff. Future tasks should replace this brief with their scope,
ownership and acceptance evidence. Do not expand deferred features or relax quality gates.
