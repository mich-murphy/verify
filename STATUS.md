# Status

Verify v1 is implemented for cooperative local use on macOS and Linux, with GitHub shipping.
The public repository is a clean MIT-licensed snapshot, not an import of development archives
or other applications' source.

## Implemented

- Deterministic checks, guards, configuration discovery, onboarding and exact-commit evidence.
- A shared bounded command runner, process-group cleanup and durable worktree ownership.
- One fresh subscription reviewer with an initial-plus-delta budget, same-commit reuse and
  persistent finding dispositions. Human decisions affect admission without rewriting run facts.
- CLI verification scenarios, status, explicit decisions and consistent lease/PR/status targets.
- Pi feedback and hook-bypass checks; documented Claude Code hook integration.

## Verification

Local release checks passed `make ci` and `make check`: formatting, lint, module tidiness,
workflow validation, secret scanning, race tests, coverage, dead-code and vulnerability checks.
Total coverage after the marker correction was **85.5%**, with unchanged
**80% total / 70% package** floors.

Eight original synthetic reviewer examples have hashed before/after source and executable
Go oracles. Tests replay their exact diffs and require the specified before/after assertion
results. Ground truth is withheld from reviewer packets. These public examples have **not**
been live-model evaluated; they are not an accuracy benchmark. See
[reviewer evaluation](docs/reviewer-evaluation.md).

The installed-Pi harness exercises real Verify subprocesses, including failure feedback,
bounded follow-ups and reset. Earlier isolated model-session testing covered repair and
settlement; neither is a claim about the interactive TUI or permanent user installation.

The [CI workflow](https://github.com/mich-murphy/verify/actions/workflows/ci.yml) runs the full
quality gate on Linux and macOS, with prek installed for integration tests. Consult its actual
run result rather than treating local evidence as remote CI approval.

## Verify rename

The executable is now `verify`, with `verify.yaml`, `VERIFY_*` environment variables,
`verify.*` JSON schemas, `<git-common-dir>/verify/` state and `verify/verified` GitHub status.
The module and repository URL were already named `verify`. This is a breaking rename;
[upgrade instructions](README.md#upgrading-from-gate) cover config, agent activation,
state and branch protection. Old Gate state is retained, not migrated.

Rename validation passed `make check`, `make fmt-check`, `make build`, uncached race tests
across all 18 packages, and the installed-Pi harness (one test, no skips). Built-CLI smoke
checks passed root help, all nine JSON command-help responses, structured usage errors and
agent-hook blocking. Coverage remains **85.5%**, with unchanged 80/70 floors. Remote CI and
live-model behavior have not been rerun for this uncommitted rename; no global install or
integration activation was performed.

## Linux marker regression

The first Linux CI run exposed a genuine ownership bug: a deleted file's inode could be reused,
so file identity alone could mistake a replacement quarantine marker for the original. Markers
now carry a unique per-acquisition token, verified with bounded content reads before release.
The original regression remains, with added same-inode, repeat-release and symlink cases.
A controlled Linux replay observed inode reuse on all 32 runs: the old code failed all 32,
and the corrected code passed all 32 under the same conditions. No test or threshold was relaxed.

## Battle qualification

Final working-tree qualification passed `make check`, all 18 packages under uncached shuffled
race testing, five focused race repetitions, and the installed-Pi harness (one pass, no skips).
Coverage is **86.4%**, with unchanged 80/70 floors. All 18 package test binaries also passed
in the isolated offline Linux arm64 image, **without race instrumentation** (CGO disabled).
Four bounded fuzz runs and serialized core benchmarks are retained. A measured >2× CLI
regression was corrected without removing lifecycle safeguards: controlled fake-tool warm check
medians at 1/10/100 files were 0.428/0.660/2.935s, versus baseline 0.531/0.902/4.682s.
These are orchestration measurements, not real hook/model/network latency. See
[battle testing](docs/battle-testing.md) for inventory, evidence, recovery and limits.
No live reviewer, real shipping or remote CI approval is claimed; parent acceptance remains separate.

## Deliberate limits

Verify is not a hostile-code sandbox, an autonomous repair agent or a CI/merge/deploy service.
The intent-based revert test and Sign-off mutation testing remain deferred. A small synthetic
review corpus does not establish general reviewer accuracy. Users configure branch protection
and install/activate integrations explicitly; Verify does not do those automatically.
