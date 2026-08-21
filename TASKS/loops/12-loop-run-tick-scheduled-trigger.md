# Scheduled trigger — new `loop_run_tick` `JobType`

**Phase:** 3 — Trigger surface (`TASKS/loops`)
**Status:** not-started
**Depends on:** `08-loop-engine-core.md`
**Touches:** `internal/scheduler/runner_adapter.go` (new `JobType` const, new payload
struct, new dispatch case), `internal/scheduler/store_adapter.go` (matching switch on the
Store side), `internal/store/migrations/` (only if `agent_schedules.job_type` has a DB
`CHECK` — confirm at dispatch time).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s scheduled trigger surface —
quoted: *"a new `loop_run_tick` `JobType`, the fifth alongside Scheduling's
already-enumerated `durable_agent_wake`/`agent_workflow_run`/`command_run`/`reflex_dispatch`
(12-scheduling.md) — for a 'durable'-preset loop or a `WAIT`-status loop polling an external
condition (the proposal's Event/Durable Loop type, §11). Depends on Scheduling's own design
landing first; not itself new engine work."* **Scheduling is fully landed** — confirmed this
session against `TASKS/INDEX.md`'s Scheduling section: all 9 tasks `reviewed`, "the
production scheduling mechanism... is fully retired; `go-scheduler`'s `Engine` is now the
live mechanism." This dependency is satisfied; nothing blocks this task.

**Real enum + dispatch switch, confirmed this session** — `internal/scheduler/runner_adapter.go:29-34`:
```go
const (
    JobTypeDurableAgentWake = "durable_agent_wake"
    JobTypeAgentWorkflowRun = "agent_workflow_run"
    JobTypeCommandRun       = "command_run"
    JobTypeReflexDispatch   = "reflex_dispatch"
)
```
Dispatch: `RunnerAdapter.Enqueue(ctx, job gosched.Job)` (`runner_adapter.go:187-201`) —
switches on `job.JobType`, each case delegates to a narrow `enqueueX` method; `default`
returns an "unknown job type" error. `store_adapter.go:201` has the matching Store-side
switch (converts an `agent_schedules` row → a `gosched.Schedule`). Adding `loop_run_tick`
means: one new const, one new case in each switch, one new narrow dependency field on
`RunnerAdapter` (the file's own convention: `Wake DurableAgentWaker`, `Workflows
WorkflowLauncher`, etc. — every job type's real dispatch dependency is a separate,
independently-nilable interface field, never a shared one).

**Payload shape, this planning session's decision** (README): `LoopRunTickPayload{LoopRunID
string}` — matches the existing four payload structs' exact convention (one required
identifier field, everything else `omitempty`; confirmed this session, all four read in
full):
```go
type LoopRunTickPayload struct {
    LoopRunID string `json:"loop_run_id"`
}
```
No `omitempty` fields needed — a tick has exactly one job, re-checking one specific
`LoopRun`'s resume condition (or, per task `11`'s own open question, re-evaluating any
`resume_loop_run` reflexes attached to it). Note also, confirmed this session: none of the
four existing job types has a real duplicate-run guard (`gosched.ErrDuplicateJob`
translation exists nowhere in this file yet) — `loop_run_tick` starts from the same
"nothing guards against a duplicate tick" baseline unless this task deliberately adds one
(recommended: a duplicate tick against an already-non-`WAIT`ing `LoopRun` should be a cheap
no-op, not an error — the tick handler checks current status first and returns early).

## What to do

1. **`JobTypeLoopRunTick = "loop_run_tick"`** — `runner_adapter.go`, alongside the existing
   four consts.

2. **`LoopRunTickPayload`** — as specified above.

3. **`RunnerAdapter` field + dispatch case** — a new field (e.g. `Loops LoopEngine` or a
   narrower interface exposing just what's needed — likely just `Resume(ctx, loopRunID
   string) error`, don't require the full `LoopEngine` interface if only `Resume` is called
   here). New `case JobTypeLoopRunTick:` in `Enqueue`'s switch, delegating to a new
   `enqueueLoopRunTick` method: unmarshal `LoopRunTickPayload`, check the `LoopRun`'s
   current status (skip if already terminal or not actually `WAIT`ing — the no-op guard
   above), call `Resume`.

4. **`store_adapter.go`'s matching switch** — the Store-side conversion from an
   `agent_schedules` row (with `job_type = 'loop_run_tick'` and a `job_payload` JSON blob
   matching `LoopRunTickPayload`) to a `gosched.Schedule`. Follow the existing four cases'
   exact shape.

5. **How a `loop_run_tick` schedule gets created** — resolve where the actual
   `agent_schedules` row with this job type comes from: task `08`/`10` should insert one
   automatically whenever a `LoopRun` enters `WAIT` status with a durable/polling
   continuation policy (the design doc's "durable"-preset case), with an interval derived
   from the preset's own polling cadence (task `13` names this). Document the actual
   call site — if task `08`/`10` didn't already build this (check their landed Work Logs
   first), add the minimal insertion call here rather than leaving `loop_run_tick` schedules
   with no real creator.

## Done means

- `loop_run_tick` dispatches correctly through both `runner_adapter.go`'s `Enqueue` switch
  and `store_adapter.go`'s conversion switch, tested against a real `gosched.Job`/`Schedule`
  round-trip (matching whatever test pattern `runner_adapter_test.go`/`store_adapter_test.go`
  already use for the other four job types).
- A tick against a `LoopRun` no longer in `WAIT` status is a confirmed no-op (tested), not an
  error.
- A tick against a genuinely `WAIT`ing `LoopRun` calls `Resume` (tested).
- The real creation path for a `loop_run_tick` schedule is identified and either already
  exists (cite the task/commit) or is built here — not left unaddressed.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
