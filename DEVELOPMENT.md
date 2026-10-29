# Developing Gate

Gate is a Go project (see `go.mod` for the version). [SPEC.md](SPEC.md) defines the
product; [STATUS.md](STATUS.md) says what is built.

## Setup

You need Go and [prek](https://github.com/j178/prek). Dev tools (gofumpt, goimports,
golangci-lint, deadcode, govulncheck, go-test-coverage, actionlint, betterleaks) are
pinned in `tools/go.mod` and run with `go tool -modfile=tools/go.mod <tool>`. Nothing
else needs installing.

Install the git hooks once:

```
prek install --hook-type pre-commit --hook-type pre-push
```

## Make targets

| Target | What it does |
|---|---|
| `make build` | Build `bin/gate`. |
| `make fmt` / `fmt-check` | Format with gofumpt and goimports / fail if unformatted. |
| `make lint` | golangci-lint (config in `.golangci.yml`). |
| `make vet` | `go vet ./...`. |
| `make tidy-check` | `go mod tidy -diff` for both modules. |
| `make test` | `go test -race -shuffle=on -count=1 ./...`. |
| `make cover` | Tests with a coverage profile, checked against `.testcoverage.yml` (total 80, package 70). |
| `make deadcode` | `deadcode -test ./...`; any output fails. |
| `make vuln` | govulncheck. |
| `make secrets` | betterleaks scan of the git history. |
| `make actionlint` | Lint the GitHub workflows. |
| `make check` | The prek `pre-commit` then `pre-push` stages, over all files. Run this before handing work back. |
| `make ci` | The same checks run directly, without prek. CI runs this on Linux and macOS. |

Hooks are defined in `.pre-commit-config.yaml`. They call the make targets, so there
is one definition of each check. Pre-commit (fast tier) runs file hygiene, fmt-check,
lint, actionlint and secrets on the staged files. Pre-push (full tier) runs tidy-check,
vet, cover (one race and coverage run, `make cover TESTFLAGS=` so Go's test cache
applies), deadcode and vuln. Each is scoped by `files`/`types` (SPEC §4.5). CI installs
prek 0.5.5 so tool-dependent integration tests run, then runs `make ci`, which keeps
`-count=1`. The installer action is pinned by commit and verifies the release checksum.

To update a tool: `cd tools && go get -tool <module>@<version>`, then
`go mod tidy`. `tools/go.mod` pins `go.yaml.in/yaml/v4` to rc.3 because actionlint
does not build against the newer release that other tools pull in. Drop the
`replace` once actionlint supports it.

## Conventions

- Standard library first. Add a dependency only when it clearly pays for itself.
- Small packages under `internal/`: agenthook, audit, check, cli, config, eligible,
  gitx, glob, guard, lane, onboard, pkgmgr, review, runner, ship, state, verify.
  `internal/lane` is pure: no `os`, `os/exec` or `net/http` (enforced by depguard).
- Table-driven tests. Test behaviour, not implementation.
- Fail closed: a missing tool, corrupt record or unknown config key is an error,
  never a pass.
- Records are `/1`. No schema version stacks and no migrations.
- Never weaken a check. Do not add `//nolint` without a linter name and a reason.
- Fix and re-run. Keep changes small and the docs short.

## Change notes

For a change ready for review, record **Testing**, **Risk**, and **How to revert**.
Name the commands actually run and any unverified behavior; describe plausible failure
modes and a realistic manual recovery path. These are repository conventions, not new
Gate policy or rollback automation. Do not turn a reviewer-written repro into a claim
of executed evidence. Explicit subscription-CLI experiments use the bounded developer
harness in [docs/reviewer-evaluation.md](docs/reviewer-evaluation.md), never ordinary CI.

## How work is coordinated

Larger changes are split between subagents with disjoint file ownership. Each gets
a brief naming the files it owns and the files it must not touch. The parent agent
reviews the result, runs the gates, and makes the commits. Workers do not commit
or push. In Pi, use the project agent type `gate-worker` (`.pi/agents/gate-worker.md`):
it has `websearch` and `webfetch`, which the built-in `general-purpose` type lacks.

The active brief lives in `docs/coordination/current-session.md`. Keep it current
when ownership or decisions change.
