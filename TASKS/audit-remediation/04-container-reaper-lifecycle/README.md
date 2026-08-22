# 04 — Container/reaper goroutine lifecycle

This folder groups every audit finding touching `internal/service/container.go`'s
background-reaper goroutines and the two `go test -race` timeouts that surfaced
during the audit's testing baseline (`docs/audits/2026-08-21-go-quality/REPORT.md`
§3 and §7), plus two adjacent findings from the composition-root cluster review
(§8.3) that share the same "who owns this goroutine" theme: an unsafe collector
hang in `DelegateAndAggregate`, and a broader package-wide gap in tracking
fire-and-forget spawns. A fifth, unrelated-in-mechanism but same-package testing
gap (two 0%-covered pure functions) is included here because it was grouped with
this cluster in the finding-to-task mapping (`FINDING-INDEX.md`), not because it
shares the lifecycle theme — see task 05 below.

**These five tasks are deliberately five separate files, not one merged
"Container lifecycle" task, because three of them are three genuinely distinct
problems that happen to look similar on the surface.** The remediation guide's
own "Container/reaper lifecycle" grouping (Wave 2) is explicit about this and
this folder follows its instruction directly: **keep distinct (1) constructor
partial-failure cleanup, (2) API tests that create Containers without shutdown,
and (3) `internal/service`'s own race timeout, whose cause was explicitly found
to be different from (2).** Concretely:

- **Task 01** (`GO-LIFE-001`) is a real constructor bug: two early-return error
  paths inside `NewContainer` leak the subagent/runtime reaper goroutines because
  their cancel funcs aren't captured until the end of the constructor. This is a
  production code fix, though its practical urgency is currently limited by
  `cmd/nanite/main.go` treating construction failure as fatal (no retry loop).
- **Task 02** (`GO-TEST-001`) is a test-hygiene-only gap: `internal/api`'s test
  suites construct `Container`s successfully (task 01's bug is not in play here)
  and simply never call `Shutdown()` on them, so 2 reaper goroutines leak per
  test and accumulate across a 46-test-file package — enough to prevent
  `go test -race ./internal/api` from completing even at 2.5x the default
  timeout. Confirmed **not** a production defect; `cmd/nanite/main.go` already
  calls `Shutdown()` correctly on the real exit path.
- **Task 03** (`GO-SVCCORE-006`) is an *investigation*, not a fix: `internal/service`'s
  own `-race` timeout looks superficially like the same problem as task 02, but is
  confirmed by exhaustive grep to have a different cause — zero of its 126 test
  files ever call `NewContainer`, so task 02's fix cannot possibly help this
  package. What actually causes it (sheer suite size × `-race` overhead, some other
  goroutine-accumulation source, or something else) is genuinely unknown and this
  task's whole job is to find out, with evidence, not to assume the answer and
  patch it.

A planner or implementer who merges these three into one "fix the Container
leak" ticket will very likely mis-scope task 03 as if it had a known fix
already in hand — it doesn't, and the guide is explicit that this distinction
must be preserved through remediation, not just through triage.

Tasks 04 and 05 round out the folder: **task 04** (`GO-SVCCORE-001` +
`GO-SVCCORE-002`) covers two goroutine-ownership problems in `internal/service`'s
`safego.Go` usage — an unsafe unbounded collector in `DelegateAndAggregate` (clear
fix, no architect decision needed) and a package-wide inconsistency in which
fire-and-forget spawns are tracked for shutdown-drain versus not (a real
architect decision, since the codebase has direct precedent — a wake-reactor
spawn already migrated to `lifecycle.Manager` after a PR review flagged this
exact risk — for only *some* sites, not all ~18-20). **Task 05**
(`GO-SVCEXEC-005`) is unrelated in root cause (pure test-coverage debt on two
deterministic config-resolution functions) but shares the package and was
grouped here by the finding-to-task mapping; it can be picked up independently
of the other four at any time.

## Task files

1. [`01-fix-container-constructor-partial-failure-cleanup.md`](./01-fix-container-constructor-partial-failure-cleanup.md) — `GO-LIFE-001`. Constructor bug: two `NewContainer` error paths leak reaper goroutines.
2. [`02-fix-api-test-container-shutdown-leak.md`](./02-fix-api-test-container-shutdown-leak.md) — `GO-TEST-001`. Test-hygiene fix: add `t.Cleanup(Shutdown)` at 9 call sites across 6 `internal/api` test files; not a production defect.
3. [`03-investigate-internal-service-race-timeout.md`](./03-investigate-internal-service-race-timeout.md) — `GO-SVCCORE-006`. **Investigation only** — `internal/service`'s own `-race` timeout has a confirmed-different, still-unknown cause from task 02's.
4. [`04-track-untracked-goroutine-spawns.md`](./04-track-untracked-goroutine-spawns.md) — `GO-SVCCORE-001` + `GO-SVCCORE-002`. Fix `DelegateAndAggregate`'s unsafe collector (clear fix); decide a package-wide tracked-vs-fire-and-forget policy for ~18-20 `safego.Go` sites (architect decision).
5. [`05-close-untested-service-config-functions.md`](./05-close-untested-service-config-functions.md) — `GO-SVCEXEC-005`. Add unit tests for two 0.0%-covered pure functions; no production code change.

Per this batch's top-level `README.md`, none of these five tasks are sequenced
against each other beyond the soft, non-blocking notes recorded in each file's
own `Depends on` line (task 02 before task 03 is a convenience-only ordering,
not a real dependency; task 04's two parts and task 05 are fully independent of
everything else in this folder). Cross-folder sequencing, prioritization, and
the Wave-0 HEAD-vs-audited-commit revalidation are explicitly a planner's job,
not done here.
