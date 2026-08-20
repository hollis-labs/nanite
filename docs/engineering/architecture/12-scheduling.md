# Scheduling

Resolves Torque tasks **CW-20260819-0004** ("build a Nanite-native scheduler... needs a library-vs-build decision and a DB-backed failure/retry-strategy design") and **CW-20260819-0006** ("verify whether FU-30's `add_schedule` reflex action already covers some or all of 0004's ground before building from scratch") — both filed against `PRJ-20260417-0002`, full text preserved in `HANDOFF.md:141-177`. Also unblocks **CW-20260819-0005** (two periodic durable audit agents, deferred pending 0004's trigger mechanism). Produced by a dedicated architecture design session (2026-08-20) that reviewed the `go-scheduler` library, Hadron's live adoption of it, Torque's own (unrelated) scheduling engine, and Nanite's existing half-built scheduling substrate before any design was proposed. **Design only — no code or schema changed in this session.**

## Where Nanite already stood

Two mechanisms already existed, independently, neither fully connected to the other:

1. **`agent_schedules` + `durable_wake.go`** — a real table (`internal/store/migrations/071_agent_schedules.sql`) with `schedule_kind` CHECK `every_n_ticks | on_tick | cron | one_shot | on_event`, and a genuine wall-clock driver: `cmd/nanite/main.go`'s `"durable-agent-wake-tick"` goroutine polls every 2 minutes and calls `DurableAgentWakeService.RunDue` (`internal/service/durable_wake.go`), which uses `robfig/cron` to check due-ness (`wakeScheduleDue`). Only `cron` and `one_shot` are live — `every_n_ticks`/`on_tick`/`on_event` and the table's own `GetDueSchedules` path (`internal/store/agent_schedules.go`) were built for an "FU-27 composer pipeline" (per the migration's own doc comment) that was never implemented; zero production callers, tests only — independently corroborated by `docs/system-audit/2026-08-17/code-architecture/09-durable-agents-runtime.md`. The live path also carries a documented fragility: a 15-minute lookback window running independent of the 2-minute poll cadence (`durable_wake.go:311-314,451-461`). Today there is exactly one production `agent_schedules` row, written only by boot-time YAML sync (`internal/service/managed_durable_configs.go:205-256`, syncing `.nanite/durable-agents/*.yaml`) — nothing else writes to this table.

2. **Reflex Action Taxonomy's `add_schedule`** (`internal/store/agent_reflexes.go:39`, `internal/agent/reflexes/executor.go:19-22,90-97`) — legal in schema, seeded into `reflex_action_kinds` by migration 124, classified "Change from today: None" in `10-reflex-action-taxonomy.md:44-55`. Entirely dormant: `Executor.ScheduleHook`'s doc comment describes inserting a row into `agent_schedules` when a reflex fires, but the hook is never assigned in `internal/service/container.go` — only `Executor.Halt` is wired there (`container.go:921`). No reflex anywhere declares `action_kind='add_schedule'`. `internal/agent/reflexes/recurrence.go` — easy to mistake for scheduling logic given the name — is unrelated: it's a fire-cooldown/debounce guard (`DefaultReflexCooldown = 15 * time.Minute`, `RecentlyFired`), not a next-occurrence calculator.

**This directly answers CW-20260819-0006**: `add_schedule` doesn't duplicate a scheduler and isn't a separate concern needing reconciliation — it was designed as a thin producer into (1), and simply never got its wire connected.

## Decision: adopt `go-scheduler`, don't build from scratch

CW-20260819-0004 named `robfig/cron`, `go-co-op/gocron`, `hibiken/asynq`, and `reugn/go-quartz` as library candidates to evaluate against the single-binary/SQLite-only, no-hard-runtime-dependency constraint. None of those were evaluated in this session, because a stronger candidate exists in-house and wasn't on that list: `github.com/hollis-labs/go-scheduler` (`/Users/chrispian/dev/hollis-labs/libs/go-scheduler`, pre-1.0 v0.1.x). It clears the same constraint — its only dependency is `robfig/cron/v3 v3.0.1` as a **library import**, not a Cerberus/launchd runtime dependency — while additionally offering:

- **A live production adopter to copy, not a cold evaluation.** Hadron (`apps/hadron`) already imports `go-scheduler` and wraps it in a ~165-line adapter (`internal/scheduler/scheduler.go` + `adapter.go`): a `storeAdapter` maps Hadron's own `schedules` table to the library's neutral `Schedule` type, a `runnerAdapter` decodes an opaque JSON payload and dispatches into Hadron's execution manager, translating duplicate-run races into the library's `ErrDuplicateJob` sentinel. `cmd/hadrond/main.go` starts/stops the engine alongside the rest of the daemon. This is a directly transferable template, not a hypothetical pattern.
- **No new external dependency footprint for Nanite specifically.** `robfig/cron/v3` is already a transitive dependency today, used independently by both `store/agent_schedules.go` and `service/durable_wake.go`. Adopting `go-scheduler` doesn't add a new library family to the dependency graph, just consolidates two hand-rolled cron call sites behind one small, tested engine.
- **A genuinely double-dispatch-safe core.** `Engine.tick()` claims each due schedule via `Store.ClaimAndUpdateScheduleRun` — a compare-and-set on the schedule's stored `next_run` (`UPDATE ... WHERE id=? AND next_run=?`, checking `RowsAffected()`). Two concurrent ticks or processes racing the same schedule row: only one wins the claim, the other sees `claimed=false` and skips, silently and safely. Nanite's current mechanism has no equivalent — the 15-minute lookback window is a heuristic substitute for real dispatch tracking, not a correctness guarantee.

**What `go-scheduler` deliberately doesn't give you**, confirmed by direct reading of `engine.go`/`scheduler.go`/`errors.go` — and each gap is addressed by a later section of this doc, not waved away:
- No retry/backoff policy: a failed `Runner.Enqueue` call causes `SetScheduleNextRun` to roll `next_run` back to the pre-claim value, so the same firing becomes due again on the very next 1-second tick — forever, no backoff, no cap, no dead-letter. → "Retry, backoff, and `on_fail` policy" below.
- No observability beyond a four-field `Status{Running, LastTickAt, Dispatches, WorkerErrors}` counter snapshot — no logging, no per-schedule error detail, no hooks. → "Observability" below.
- No timezone awareness — cron parsing and the engine's `now` are both naive UTC. → "Timezone" below.
- Tick interval (`1 * time.Second`) and per-tick batch size (`100`) are unexported constants in the library, not configurable via any constructor option. → "Tick cadence and batch size" below.
- Zero built-in `Store` implementation — entirely BYO persistence; the library's own tests use in-memory fakes only. → "The Store adapter" below.

## Full replace, not dual-run

`go-scheduler`'s `Engine` **replaces** the `"durable-agent-wake-tick"` 2-minute ticker and `wakeScheduleDue`'s manual due-check in full — it does not run alongside the existing mechanism. Running both would mean two independent pollers racing the same `agent_schedules` rows with two different correctness models (one CAS-based, one lookback-window-based); there's no scenario where that's safer than one engine with one claim mechanism. The 15-minute-lookback fragility is retired as a side effect, not patched separately — the CAS claim is what makes a lookback window unnecessary in the first place.

`schedule_kind` collapses from five values to two: **`cron` | `one_shot`**. `every_n_ticks`/`on_tick`/`on_event` are dropped from the CHECK constraint entirely, not kept as unimplemented schema slots. Reasoning: `go-scheduler`'s model only understands time-based triggers (a `CronExpr`, or empty-string-for-one-time); `every_n_ticks`/`on_tick` were message-count-based (the reflex evaluator's `TickN`/`EveryNTicks` concept, `internal/agent/reflexes/evaluator.go:31-41` — a fundamentally different axis, already implemented correctly there), and `on_event` is event-driven rather than time-driven. None of the three map onto a tick-and-claim engine, none had a production caller, and the composer that was meant to consume them was never built. Keeping the schema slots "for later" would just recreate exactly the ambiguity `CW-20260819-0006` was filed to resolve — a table that looks live but isn't.

## The Store adapter

`go-scheduler.Store` requires four methods (`ListDueSchedules`, `ClaimAndUpdateScheduleRun`, `SetScheduleNextRun`, `DisableSchedule`), and the CAS claim's correctness is entirely the implementer's responsibility — the library provides no reference implementation or contract test. This is the single biggest risk the library review flagged. It's substantially de-risked in Nanite's case: `internal/store/store.go`'s `New()` already opens the database via `sqlitekit.OpenSingle(ctx, absPath, sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions(), ...})` — a **single-connection pool** with WAL, `busy_timeout(5s)`, and `_txlock=immediate` applied on every connection (`store.go:35-39`). This is the same pattern Torque's `docs/architecture/sqlite-concurrency-pattern.md` documents and explicitly names as "the canonical reference for Nanite, Vanta, Hadron, and other local-first apps" — Nanite already has it, unlike Torque (which layers its own `writeq` serializer on top instead). A single writer connection means `UPDATE agent_schedules SET last_run=?, next_run=? WHERE id=? AND next_run=?` checking `RowsAffected()==1` is safe by construction, in-process and cross-process — no new write-serialization infrastructure needs to be built for this.

## The Runner adapter and job taxonomy

`go-scheduler.Runner.Enqueue(ctx, Job)` is the single dispatch seam; `Job.Payload` is opaque `[]byte` (JSON by convention, per the library's own doc comments) and `Job.JobType` is an opaque string the Runner switches on — matching Hadron's pattern exactly. Four job types for v1, chosen to cover "agents, commands, etc." from the outset rather than only replacing today's one live use case:

| `JobType` | Dispatches into | Notes |
|---|---|---|
| `durable_agent_wake` | `DurableAgentWakeService`'s existing wake-prompt path | Direct replacement of the only live production use case today — no new capability, just relocated onto the new engine. |
| `agent_workflow_run` | `internal/service/workflow_launch.go` / `internal/api/workflows.go`'s existing launch surface | Confirmed to exist as a distinct launch path from durable-agent-wake before including it here — not speculative. |
| `command_run` | Self-tool / CLI command execution | Generic payload: command name + args. Covers CW-20260819-0005's periodic audit agents. |
| `reflex_dispatch` | Evaluates/applies a specific reflex outside the normal per-chat-turn pass | For reflex logic that needs to run on a timer rather than only on message activity. |

## Retry, backoff, and `on_fail` policy

Since `go-scheduler` itself only offers "retry the same firing every second, forever, uncounted," the actual retry/backoff/failure policy CW-20260819-0004 asked for has to live entirely in the Runner adapter, not the library. Design, borrowing Torque's `retry_count`/`max_retries`/`on_fail` vocabulary (`internal/runtime/scheduler/lifecycle.go`) and adapting it from Torque's single-dispatch-task model to a recurring-schedule context:

- A new table, illustrative shape (architecture-level agreement, not migration-ready DDL — matching this repo's own convention for docs at this stage): `schedule_runs(id, schedule_id, run_id, fired_at, status, attempt_count, last_error, next_attempt_at)`, one row per firing (`Job.RunID`), tracking retry state across that firing's attempts.
- On `Enqueue` failure, the Runner checks `schedule_runs` for the current firing: if `attempt_count < max_retries` (a per-schedule column, mirroring Torque's per-task `max_retries`) and `now < next_attempt_at` hasn't elapsed yet, it returns the same error again immediately without a real dispatch attempt — cheap, so `go-scheduler`'s 1-second hammering costs nothing beyond a table lookup between real attempts, while the actual backoff cadence is owned by `next_attempt_at`, not the library's tick rate.
- Once `attempt_count >= max_retries`, the Runner applies the schedule's `on_fail` policy (`retry` doesn't apply here by definition; `disable` calls `DisableSchedule` directly, `notify` emits an event without disabling) and returns `nil` — signaling success to `go-scheduler` purely to stop its retry loop, with the real outcome recorded in `schedule_runs`, not misrepresented to the engine as a successful dispatch.

The backoff curve itself (linear vs. exponential, specific intervals) is not decided in this session — see "What this session did not decide."

## Producers

Three producers write `agent_schedules` rows going forward, plus the operator surface:

1. **Boot-time YAML sync** (`managed_durable_configs.go`) — unchanged, remains the mechanism for declaratively-configured agent schedules.
2. **Agent self-tool** — a new `nanite_*`-namespaced self-tool (the namespace already reserved for first-party tools per this repo's tool-naming convention) letting an agent schedule its own follow-up work. Per `11-harness-reactive-self-tools.md`'s resolved package placement, new self-tool surface belongs in `internal/selftools`, not `internal/mcp`.
3. **`add_schedule` reflex, wired** — `Executor.Schedule` gets assigned in `container.go`, mirroring the existing `Executor.Halt` wiring at `container.go:921`, so a firing `add_schedule` reflex genuinely inserts an `agent_schedules` row instead of only staging a logged-only `AppliedAction`. This is the concrete fix that closes CW-20260819-0006's loop.
4. **Operator HTTP API** — `/api/schedules` CRUD, mirroring Hadron's `/v1/schedules` surface, for operator/UI-driven schedule management outside of agent or reflex control.

## Observability

`go-scheduler`'s own `Status{}` counters are too coarse to rely on alone (four monotonic counters, no per-schedule detail, no logging anywhere in the library). Rather than building a second observability surface, schedule fires reuse the existing reflex `event_log`/telemetry pattern (`EmitFirings`-style, `internal/agent/reflexes/telemetry.go`) — one consistent place operators already look for "did this fire, when, why," rather than a third place to check. `Engine.Status()` is still worth exposing (e.g. via the `/api/schedules` surface) as a coarse liveness signal, but it supplements per-firing event rows, it doesn't replace them.

## Timezone

UTC-only for v1. `go-scheduler` has no timezone awareness natively (`cron.ParseStandard`, no location-aware variant used), and no stated need for local-time schedules surfaced in this session. Revisit only if a real "9am in the user's local time" requirement appears.

## Tick cadence and batch size

`tickInterval = 1s` and `dueBatchLimit = 100` are unexported constants inside `go-scheduler`, not configurable from the consuming app. Acceptable given current and near-term expected schedule volume (single-digit rows in production today, low tens expected once the four producers above are live) — 1-second polling against a single-connection SQLite store at that volume is negligible overhead. If schedule volume ever grows enough for this to matter, the fix is a library change (fork or upstream PR to `go-scheduler`) — not an app-side workaround, since the constants aren't reachable from outside the package.

## What this session did not decide

Listed explicitly, matching this repo's convention for architecture-level docs, so omission isn't mistaken for a locked decision:

- Exact schema DDL for `schedule_runs` and the `agent_schedules` column changes (dropped `schedule_kind` values, added `max_retries`/`on_fail` columns) — illustrative shape only, above.
- The retry backoff curve (linear vs. exponential, specific interval values) — the mechanism (Runner-owned, `schedule_runs`-tracked, throttled independent of the library's 1s tick) is decided; the curve is not.
- The exact name of the new agent-facing scheduling self-tool.
- The `/api/schedules` auth/permission model — whether it needs provenance-tier-style gating analogous to reflexes (`10-reflex-action-taxonomy.md`'s Facet 3), or standard operator auth is sufficient.
- Whether `reflex_dispatch` job firings should call into `reflexes.Resolve(...)` (the shared decision engine proposed in `10-reflex-action-taxonomy.md`, itself not yet implemented) once that lands, versus a standalone invocation path in the meantime.
- Whether `command_run` needs sandboxing/permission scoping beyond what normal self-tool execution already provides.
- CW-20260819-0005's two specific audit-agent definitions — this design unblocks them (via `command_run` or `durable_agent_wake`) but doesn't scope them.
- What happens if `go-scheduler`'s hardcoded tick/batch constants ever need to change — noted as a fork-or-upstream-PR problem, not designed further here.

## Status

Design complete as of 2026-08-20. No code or schema changed. Implementation — the `Store`/`Runner` adapter, the `schedule_runs` table and retry logic, the `Executor.Schedule` wiring, the new self-tool, and the `/api/schedules` surface — is deferred to a planning session.

**Update, 2026-08-20 (later session):** that planning session ran — implementation tasks filed at `TASKS/scheduling/README.md` (9 task files across 2 phases, planning-only, not yet dispatched).
