# Battle testing and simplification

This local qualification covers the working-tree changes based on `ee4777d`, not remote CI
or a live-model evaluation. Retained development evidence is under
`/tmp/verify-battle-review/{evidence,execution,surfaces,independent,performance-fix,qualification}`;
these temporary artifacts are not shipped or permanent public links. The qualification logs
retain exact commands, tool versions, source manifests, fixtures and all timing samples.
Parent review owns acceptance. Findings below are executed local evidence or explicitly
labelled source-review inference, not externally certified correctness.

## Inventory and rationale

The independent inventory reconciled 17 internal packages, 51 production Go files including
the command entry point, and 84 named Go types. This is review coverage, not exhaustive
behavioral coverage. Small ordered slices and transient maps remain appropriate; no new
framework, persistent cache, dependency, schema or database was justified.

| Package / surface | Structures, invariant and scaling rationale |
|---|---|
| state | Typed raw records, findings, decisions and ordered steps preserve provenance and execution facts. Atomic writes, locks and reversible names are safety mechanisms. Duplicate/coherence maps are transient O(record size); decision history grows across commits. |
| eligible | Explicit Input/Result and a small evaluator separate policy from IO. O(F) identity maps project dispositions without changing immutable claims; decision scans can reach O(FD). Ordinary review responses are capped at 100 findings; no persistent index justified. |
| review | Restricted packet/prior-finding types prevent author reasoning/check results leaking into review. Strict raw response types distinguish missing/null fields. Ordered slices, 256 KiB packet and bounded subprocess capture are retained; packet size is checked after construction. |
| ship | Resolved target pairs bind lease, push and GitHub repository. Three ordered actions retain dry-run/partial-progress behavior. Presentation copies show decisions beside raw evidence; the shared runner replaces duplicate unbounded gh capture. |
| runner | Bounded stream captures, process-result/cause records, per-instance process operations and sticky quarantine encode real lifecycle distinctions. An ESRCH-only group probe avoids unnecessary process-table scans; uncertain results still inspect and fail closed. |
| gitx | Repo owns one reusable runner, a cancellable capacity-one command gate and sticky unsettled latch. Exact diff/path/mode records and durable token/content/identity markers protect reads and worktree reuse. Per-stream Git capture is 16 MiB. No mutable-ref/content cache added. |
| check | Ordered guard/prepare/stage records and one scope computation keep exact-commit evidence distinct from fast local feedback. Scope classification is linear in changed paths. Shared worktree ownership must remain. |
| verify | Scenario/result types distinguish expected nonzero exits, startup failure, output uncertainty and assertions. Setup/scenarios remain ordered slices; a definite mismatch wins over uncertainty. |
| guard | Small rule tables, line scans and dedup sets suit diff-sized inputs. Generated checks inspect base regular files, including deleted entries; Git subprocess reads, not just pure scans, affect latency. |
| lane | Pure ordered path/config classification, no filesystem/process access. Linear path/pattern work is sufficient. |
| glob | Small glob matcher avoids a new dependency or persistent pattern cache; benchmark growth remains workload-dependent. |
| config | Typed config, duration and provenance records; pointer assertions distinguish absent from explicit empty. Ordered commands/scenarios and duplicate-name maps preserve semantics; base selection has at most two merge-base candidates. |
| audit | A fixed obligation/hook-rule table and ordered report describe about 15–25 obligations. Partial config projections and bounded-depth JSONC inheritance avoid a rule engine; text-based tool-config detection remains heuristic. |
| onboard | YAML planning tree plus append-only source preservation retain comments/order. Tiny definition maps and used-ID sets prevent duplicate hooks. Repeated entrypoint discovery could be simplified later, but was not a measured bottleneck. |
| pkgmgr | Fixed five-manager table plus package script/dependency maps centralize discovery and argv construction. Immediate-directory discovery is deliberately not a recursive workspace model. Shared argument quoting fixes nested-directory execution. |
| agenthook | Bounded input projection, fixed membership maps and depth-limited lexical screening match the cooperative noise-reduction threat model; this is not a shell sandbox. |
| cli / cmd | Small command registry, shared rendering and snapshot view preserve one machine contract. Removed unused loadRecords parameters; no speculative registry rewrite. |
| Pi / scripts / build | Bounded feedback counters/output remain small state. Formatting stays check-only. CI now uses one race+shuffle+uncached coverage suite, not duplicate test and coverage executions; local hook caching and coverage floors remain. |

## Demonstrated corrections

- Conflicting same-ID branch findings could replace immutable review severity/claim facts.
  Projection now changes only coherent disposition; current conflicting evidence fails closed,
  including with acceptance/parked approval. PR bodies retain accepted guard claims and raw results.
- Actual Git quoted mode/binary headers and trailing whitespace lost exact path identity.
  Parsing now preserves them or rejects unsupported forms. Nonregular committed policy stays an
  error; only the generated guard intentionally skips nonregular base content. Generated regular
  file replacement by symlink remains blocked. Deleted-test findings no longer duplicate IDs.
- Truncated output can prove a contains assertion impossible when the needle exceeds coherent
  total output length; that is a mismatch, not waivable uncertainty.
- Audit accepted warning/off/error-prefix flags as error-level enforcement. Complete flag tokens
  and later overrides fix the demonstrated cases. Generated onboarding commands now execute in
  directories containing spaces, apostrophes and semicolons. Supported `.pre-commit-config.yml`
  is protected by the agent hook.
- Fake gh/Git descendants held pipes past cancellation; old remote Git capture accepted oversized
  output. Both now use bounded shared foreground execution. Ordinary Git exit 1/2 remains distinct
  from cancellation/overflow/unsettled cleanup. PR view overflow cannot trigger create fallback.
  Sticky quarantine prevents destructive worktree fallback and marker removal after late failure.
- The first migration introduced a real performance regression. An ESRCH-only process-group
  shortcut and direct read of already validated immutable FileAt metadata remove redundant work
  without weakening lifecycle or policy checks. Existing FileAt reads use three Git commands,
  rather than five; no batching/cache was introduced.

An intermediate FileAt change incorrectly treated nonregular policy as absent. Parent review
caught the cross-caller regression; explicit ErrNonRegularFile plus actual config.Load symlink
regressions corrected it before final qualification. This history is not hidden as a clean first pass.

## Executed qualification

macOS 27.0.1 arm64 / Apple M2, Go 1.27.3, Git 2.56.0, prek 0.5.5:

- Final `make check` passed formatting, lint, hygiene, workflow validation, secrets, tidy, vet,
  race+coverage, deadcode and vulnerability checks. Coverage: **86.4%**, unchanged 80% total /
  70% package floors.
- `go test -race -shuffle=on -count=1 -timeout=180s ./...` passed all 18 packages.
- `go test -race -count=5 -timeout=180s` across eligible/runner/gitx/guard/ship/verify with
  `-run '^(TestBattle|TestFileAt|TestProcessGroup|TestGroupProbe|TestOSBoundary|TestBoundedGroup)'`
  passed, including final optimization regressions.
- Four earlier bounded fuzz runs (`-run '^$' -fuzz '^FuzzBattleNAME$' -fuzztime=3s
  -parallel=2 -timeout=30s`) passed: config/Parse 96,154 executions, audit/StripJSONC 62,133,
  agenthook/Evaluate 155,050 and gitx/QuotedHeader 83,848. Their parser/test files are unchanged
  by the performance correction; final full/race tests cover the changed Git execution boundary.
  These are bounded robustness experiments, not proofs or throughput claims.
- Installed Pi harness: `PI_PACKAGE=<installed pi 1.4.0 package> node --test
  integrations/pi/verify.test.cjs`: one passed, zero skipped, real Verify subprocesses,
  no model call. Not interactive TUI or permanent activation evidence.

Linux arm64 used only existing `gate-review-linux:e6d9813`, no pull/network or credential mounts,
with source/tests/toolchain read-only and writable temporary storage. All 18 package test binaries
passed uncached/shuffled with **CGO_ENABLED=0, no race instrumentation**. Git 2.39.5, prek 0.5.5,
Go 1.27.3. The first attempt stopped at corpus oracle launches because Go was absent, not because
of a product failure. The authorized rerun used the official Linux Go distribution verified against
SHA256 `a06e99309dbcbdca1a8ccd2282c3ee146e55930886e26b4f1193269587165f49`
from the [fetched official release metadata](https://go.dev/dl/?mode=json), mounted read-only.
No corpus exclusions or fake compiler were used; container networking stayed disabled.

## Measured performance

Actual CLI, identical disposable fixtures with real Git and **controlled fake prek/probe**.
No remote, reviewer or ship. Each size had one cold and three warm check→verify→status passes,
plus fast-check full-record immutability assertions. Cold means new Verify state/worktrees,
**not cold OS/tool caches**. Build/download time is excluded. Baseline is archived `ee4777d`;
pre-fix is the bounded-runner migration before optimization. Same-window check seconds:

| Modified regular .md files | Baseline warm median | Pre-fix warm median | Fixed cold | Fixed warm median [range] |
|---:|---:|---:|---:|---:|
| 1 | 0.5306 | 1.3177 | 0.9278 | 0.4278 [0.4215–0.4300] |
| 10 | 0.9019 | 2.3970 | 0.6936 | 0.6598 [0.6596–0.6636] |
| 100 | 4.6818 | 13.3119 | 2.9704 | 2.9347 [2.9342–2.9589] |

The >2× regression was rejected, not described as fast. Fixed check is 19–37% faster than
baseline in this small experiment; both optimizations were measured together, not separately
attributed. Fixed warm verify medians were 0.3130/0.3252/0.3175s and status
0.1886/0.1937/0.1961s. Independent final 100-file rerun passed: cold 3.2827s,
warm median 2.9565s [2.9411–2.9958]. A fresh final build was byte-identical to the measured
fixed binary; source manifests match the independently reviewed optimization.

Core benchmarks used `-run '^$' -bench '^BenchmarkBattle' -benchmem -benchtime=200ms
-count=3 -timeout=120s`, one package at a time. Selected median [range]:

| Operation | Time |
|---|---:|
| Final runner foreground true (real subprocess) | 1.677ms [1.650–1.749] |
| Final Git diff parser, 10,000 files | 2.324ms [2.298–2.324] |
| Guards pure large diff, 10,000 files | 16.036ms [15.681–16.130] |
| Scope classification, 10,000 paths | 0.444ms [0.444–0.450] |
| Lane classification, 10,000 paths | 3.551ms [3.547–3.571] |
| YAML parse, 1,000 scenarios | 3.789ms [3.759–3.813] |
| Eligibility filter, 1,000 decisions | 31.628µs [31.489–34.150] |
| Packet build, 250 KiB | 31.304µs [29.979–31.402] |

Three samples plus allocation results for all eleven benchmark packages are retained.
Runner/gitx were refreshed after optimization; unaffected core benchmark source stayed unchanged.
Pure scans exclude Git, real checks, dependencies and reviewer cost. Remaining per-file Git work
still makes 100 files materially slower than one; these data do not justify a universal latency claim.

## Risk, recovery and limits

No live reviewer accuracy, real GitHub shipping, remote CI approval, real package installation
workload, cold filesystem cache, Linux race or other CPU architectures were qualified here.
Existing synthetic corpus oracles ran, but do not establish live-model accuracy. No check/test/
threshold was weakened. Verify is cooperative process ownership, not hostile-code/daemon containment.

Source-review hypotheses remain distinct from demonstrated unclosed failures: reviewer crash
between attempt/slot/result publication; unbounded historical state/intent allocation; conservative
quoted shell-flag handling and heuristic commented/disabled tool-config recognition; doctor/onboarding
subprocess and Pi descendant boundaries; nonancestor review recovery and newline-path rejection.
These need focused fixtures/policy decisions, not speculative fixes or claims of resolved coverage.

On corrupt/inconsistent evidence, inspect and regenerate affected records rather than editing facts
to pass. On unsettled worktree ownership, inspect and stop surviving processes **before** explicitly
removing the named marker; never auto-clear it to retry. A rollback of the lifecycle migration must
revert coordinated runner/gitx/check/verify behavior together, not remove only quarantine/markers.
The small performance correction can be reverted independently with its test-seam changes, at the
cost of the measured slowdown. No commits, history rewrites, global installation or real shipping
were performed by qualification; disposable fixture commits only.
