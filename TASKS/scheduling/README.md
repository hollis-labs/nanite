# Scheduling — implementation

Implements `docs/engineering/architecture/12-scheduling.md`, the design produced by a dedicated architecture design session (2026-08-20, operator-signed-off) that reviewed `go-scheduler`, Hadron's live adoption of it, Torque's own scheduling engine, and Nanite's existing half-built scheduling substrate before proposing anything. That session changed **no code or schema** — design only, and its own "Status" section explicitly deferred implementation to a planning session. This folder is that planning session's output.

**Not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence.** A sibling to `TASKS/reflex-taxonomy/` and `TASKS/harness-reactive-self-tools/`, not nested inside either — kept in its own top-level `TASKS/` subfolder for the same reason those are: this work wasn't part of the original plan. Resolves Torque tasks `CW-20260819-0004` and `CW-20260819-0006`, unblocks `CW-20260819-0005` (full text preserved in `HANDOFF.md:141-177`).

## Read before starting any task here

1. `docs/engineering/architecture/12-scheduling.md` — the full design: why `go-scheduler` over building from scratch, why it's a full replace of the existing 2-minute ticker (not a dual-run), the `schedule_kind` collapse from five values to two, the Store adapter's safety argument (Nanite's single-connection SQLite pool), the four-type job taxonomy, the Runner-owned retry/backoff/`on_fail` policy design, the four producers, the observability decision (reuse reflex `event_log` telemetry), UTC-only scope, and the explicit "what this session did not decide" list.
2. `/Users/chrispian/dev/hollis-labs/libs/go-scheduler/scheduler.go` and `engine.go` — the actual library interfaces (`Store`, `Runner`, `Schedule`, `Job`, `Engine`) every task below implements against. Read the real source, not just the design doc's paraphrase — exact method signatures matter here.
3. `/Users/chrispian/dev/hollis-labs/apps/hadron/internal/scheduler/adapter.go` and `scheduler.go` — the directly-transferable adapter template `02`/`03` are expected to follow (`storeAdapter`/`runnerAdapter` shape, `ErrDuplicateJob` translation, the thin `New()` wrapper). Not a hypothetical pattern — a live production adopter to copy.
4. `docs/engineering/architecture/11-harness-reactive-self-tools.md` and `TASKS/harness-reactive-self-tools/` — the self-tool package placement (`internal/selftools`, not `internal/mcp`) that `08-agent-self-tool.md` depends on. **Real cross-batch dependency, not just a reference read** — see `08`'s own Depends-on note.
5. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer discipline, and escalation rules every task file below follows.

## What this batch does NOT do

Per the design doc's own explicit "What this session did not decide" list — do not expand any task below to cover these without a fresh operator conversation:

- **Exact schema DDL beyond what each task file specifies.** The design doc is architecture-level agreement (illustrative shapes for `schedule_runs` and the `agent_schedules` column changes), not migration-ready schema.
- **The retry backoff curve's specific values** (linear vs. exponential, specific intervals) — `04-retry-backoff-on-fail-policy.md` builds the mechanism (Runner-owned, `schedule_runs`-tracked, throttled independent of the library's 1-second tick); the curve's concrete numbers are an implementation call for that task's worker to make and document, not a re-litigation of the design.
- **The `/api/schedules` auth/permission model's final shape** — whether it needs provenance-tier-style gating analogous to reflexes, or standard operator auth is sufficient. `09-operator-http-api.md` makes a concrete, documented call but it isn't a locked security decision.
- **Whether `reflex_dispatch` job firings call into the shared reflex decision engine** (`10-reflex-action-taxonomy.md`'s `Resolve()`) once that lands, versus a standalone invocation path meanwhile — `03-runner-adapter-and-job-taxonomy.md` makes a documented interim call, not a final one.
- **`command_run`'s sandboxing/permission scoping beyond what normal self-tool execution already provides.**
- **CW-20260819-0005's two specific audit-agent definitions.** This batch unblocks them (via `command_run` or `durable_agent_wake`) but doesn't scope or build them.
- **Changing `go-scheduler`'s hardcoded tick interval or batch size.** Out of reach from the consuming app (unexported constants) — a fork-or-upstream-PR problem, not something any task here attempts.

## Task sequence

**Phase 1 — Core engine.** Adopts `go-scheduler` as the tick/claim engine and retires the existing ad hoc mechanism in full. Strict-ish order — each task is a real prerequisite for the next, not just merge-conflict avoidance.

| Task | Depends on |
|---|---|
| `01-schema-schedule-kind-collapse-and-retry-columns.md` | none |
| `02-store-adapter.md` | `01` |
| `03-runner-adapter-and-job-taxonomy.md` | none directly (parallel-safe with `01`/`02` — different file surface); needed by `04` |
| `04-retry-backoff-on-fail-policy.md` | `01`, `02`, `03` |
| `05-engine-wiring-and-full-replace.md` | `02`, `04` |

**Phase 2 — Observability & producers.** Layered on top of Phase 1's engine; the four tasks below are mutually parallel-safe (different files/subsystems each) once Phase 1 lands.

| Task | Depends on |
|---|---|
| `06-schedule-fire-telemetry.md` | `03` (sequence after `05` for live verification, not a hard code dependency) |
| `07-wire-add-schedule-reflex.md` | `02` |
| `08-agent-self-tool.md` | `02`; **cross-batch: `TASKS/harness-reactive-self-tools/01-move-self-tools-to-internal-selftools.md`** — see this task's own note |
| `09-operator-http-api.md` | `02` |

**Numbering note, cross-batch:** `01`'s new migration and `TASKS/harness-reactive-self-tools/02-reactive-layer-schema.md`'s new migration were both authored against the same "latest existing migration" baseline (`125_reflex_action_kind_provenance_allow.sql`) and both provisionally claim the next number. Whichever of the two batches is dispatched second must re-check the actual latest migration on disk and renumber accordingly — see `01`'s own Context for the explicit flag.

See `TASKS/INDEX.md`'s own new section for status tracking as these land.
