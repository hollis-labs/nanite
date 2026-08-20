# Wire the `add_schedule` reflex hook — closes CW-20260819-0006's loop

**Phase:** 2 — Observability & producers (`TASKS/scheduling`)
**Status:** not-started
**Depends on:** `02-store-adapter.md` (needs `InsertAgentSchedule` to accept the new `01`-added columns correctly — `job_type`/`job_payload`/`max_retries`/`on_fail`/`next_run` — so a reflex-inserted row is a genuinely valid, dispatchable schedule, not just schema-valid).
**Touches:** `internal/service/container.go` (assigns `reflexEngine.Executor.Schedule`, mirroring the existing `Executor.Halt` assignment at `container.go:921`).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "Producers" section, item 3, and — more directly — the doc's own "Where Nanite already stood" section: `add_schedule` is legal in schema, seeded into `reflex_action_kinds` by migration 124, but **entirely dormant**. Confirmed directly against this checkout (not assumed):

- `internal/agent/reflexes/executor.go:19-22` declares `ScheduleHook` and `:34` declares the `Executor.Schedule` field.
- `internal/agent/reflexes/executor.go`'s `Apply` method already has a real, correct dispatch case: `case store.ReflexActionAddSchedule: if e.Schedule != nil { e.Schedule(ctx, reflex.AgentID, spec) } else { /* staged only, logged */ }` — the *call site* has always been correct. Only the hook assignment is missing.
- `internal/service/container.go:921` shows the exact pattern to mirror: `reflexEngine.Executor.Halt = func(ctx, sessionID, reason, evidence) error { ... }`, assigned right after `reflexEngine := reflexes.NewEngine(...)` is constructed and plugin hooks are wired.

**This is the concrete fix that closes `CW-20260819-0006`'s loop** — the design doc's own finding that `add_schedule` "doesn't duplicate a scheduler and isn't a separate concern needing reconciliation — it was designed as a thin producer into [`agent_schedules`], and simply never got its wire connected."

## What to do

1. In `internal/service/container.go`, immediately alongside the existing `reflexEngine.Executor.Halt = ...` assignment (`:921`), add `reflexEngine.Executor.Schedule = func(ctx context.Context, agentID string, spec map[string]interface{}) error { ... }`.
2. The hook's body: translate `spec` (the reflex's `action_spec` JSON, already unmarshaled by `Executor.Apply` before the hook is called — `internal/agent/reflexes/executor.go`'s `Apply` method, `spec := map[string]interface{}{}`) into a `store.AgentSchedule` and call `cfg.Store.InsertAgentSchedule(ctx, ...)`. Decide the exact `spec` field names a reflex author would write in `action_spec` JSON to describe a schedule (e.g. `{"name": "...", "schedule_kind": "cron", "schedule_spec": "0 9 * * *", "job_type": "durable_agent_wake", "body": "...", "max_retries": 3, "on_fail": "retry"}`) — document your chosen field names, since nothing existing pins this down (no reflex today declares `action_kind='add_schedule'`, so there's no existing `action_spec` shape to match against).
3. Compute `next_run` at insert time (reuse `02`'s `gosched.NextRun`-based helper for `cron`-kind specs, or the equivalent one-shot due-time parsing) — an inserted row with no `next_run` would never fire under the new CAS-claim engine, silently defeating the whole point of wiring this hook.
4. Apply reasonable defaults for any `spec` field the reflex author omits (e.g. `max_retries`/`on_fail` falling back to `01`'s column defaults if not explicitly overridden in `spec`) — don't require every reflex-authored schedule to specify every retry-policy field explicitly.
5. Log/event a failure the same way the existing `Executor.Schedule hook failed` warning already does (`internal/agent/reflexes/executor.go`'s existing `e.Logger.Warn("reflex schedule hook failed", ...)` on hook error) — no new error-handling shape needed here, the call site already handles a hook error correctly; this task only needs to make the hook itself correct.

## Done means

- A regression test proves: a reflex firing with `action_kind='add_schedule'` and a well-formed `action_spec` produces a real `agent_schedules` row with a correctly-computed `next_run`, discoverable by `02`'s `ListDueSchedules` on the next tick.
- A regression test proves a malformed/incomplete `action_spec` fails cleanly (hook returns an error, logged per the existing warning path) rather than inserting a broken row.
- A live dogfeed: fire a real `add_schedule` reflex against a scratch instance (a reflex with a near-future `cron`/`one_shot` target), confirm the resulting `agent_schedules` row is picked up and actually fires through the engine built by `01`-`05`.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your `action_spec` field-name shape (step 2) is documented in this file's Work Log.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
