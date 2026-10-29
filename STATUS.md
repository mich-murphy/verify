# Status

Gate v1 is implemented for cooperative local use on macOS and Linux, with GitHub shipping.
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
Total coverage was **85.6%**, with unchanged **80% total / 70% package** floors.

Eight original synthetic reviewer examples have hashed before/after source and executable
Go oracles. Tests replay their exact diffs and require the specified before/after assertion
results. Ground truth is withheld from reviewer packets. These public examples have **not**
been live-model evaluated; they are not an accuracy benchmark. See
[reviewer evaluation](docs/reviewer-evaluation.md).

The installed-Pi harness exercises real Gate subprocesses, including failure feedback,
bounded follow-ups and reset. Earlier isolated model-session testing covered repair and
settlement; neither is a claim about the interactive TUI or permanent user installation.

The [CI workflow](https://github.com/mich-murphy/verify/actions/workflows/ci.yml) runs the full
quality gate on Linux and macOS, with prek installed for integration tests. Consult its actual
run result rather than treating local evidence as remote CI approval.

## Deliberate limits

Gate is not a hostile-code sandbox, an autonomous repair agent or a CI/merge/deploy service.
The intent-based revert test and Sign-off mutation testing remain deferred. A small synthetic
review corpus does not establish general reviewer accuracy. Users configure branch protection
and install/activate integrations explicitly; Gate does not do those automatically.
