# `LoopLauncher` + CRUD/launch/escalation-resolution API

**Phase:** 3 — Trigger surface (`TASKS/loops`)
**Status:** not-started
**Depends on:** `08-loop-engine-core.md`
**Touches:** new `internal/loop/launcher.go`, new `internal/api/loops.go`, `internal/api/api.go`
(route registration).

## Context

Implements two of `docs/engineering/architecture/21-loops.md`'s named trigger surfaces:

- **Manual/API launch** — *"`LoopLauncher.Launch`, mirroring `WorkflowLauncher.Launch`."*
  Also covers Goal CRUD (a Goal can be authored standalone, before any loop launches
  against it, per Decision 2) and `LoopLaunchRequest` accepting either an existing `goal_id`
  or an inline goal spec the launcher upserts first, per the design doc's own Ralph-ergonomics
  note.
- **Human resolution of `waiting_on_escalation`** — *"an operator resolve call structurally
  identical to gate resolution..., calling `LoopEngine.Resume` with an optional decision
  override (force `COMPLETE`, force `CANCEL`, or supply a replan)."* Real precedent,
  confirmed this session: A2A's `ProvideTaskInput` → `resumeWorkflowRun`
  (`internal/service/a2a_task_manager.go:694-762`) — looks up the task, finds the waiting
  gate (`store.GetWaitingGates`, picks `gates[0]` — the file's own comment notes this is a
  simplification, "In the future, we could support targeting a specific gate by step_id"),
  resolves it, resumes. This task's escalation-resolution endpoint is the `LoopRun`-level
  analog: given a `loop_run_id` in `waiting_on_escalation` status, an operator supplies
  either nothing (resume normally, `LoopEngine.Resume` re-enters `Decide`) or an override
  (`force_complete`, `force_cancel`, or a replan payload).

**This planning session's decisions this task enforces** (full reasoning in the README):
one active `LoopRun` per `goal_id` (task `08` may already enforce this in `Run` — this
task's `LoopLauncher.Launch` either delegates straight to `LoopEngine.Run` and inherits the
check for free, or duplicates it at the API boundary for a friendlier error before any DB
write — prefer delegating, avoid duplicating the check in two places); standard operator
auth, no new provenance-tier gating (matches Scheduling's own `09-operator-http-api.md`
call — check that task's actual landed auth middleware choice and mirror it, don't
reinvent).

## What to do

1. **`internal/loop/launcher.go`** — `LoopLauncher` struct wrapping `*LoopEngine` (task
   `08`) and `*store.Store`. `LoopLaunchRequest{GoalID *string, InlineGoal *GoalSpec,
   DefinitionName string, BudgetOverrides *Budget}` (mirrors the design doc's own framing —
   `GoalSpec` is whatever subset of `Goal`'s fields a caller can supply inline). `Launch(ctx,
   req) (*LoopResult, error)`: if `InlineGoal` set, `CreateGoal` first; then call
   `LoopEngine.Run`. `Cancel(ctx, loopRunID) error` (sets `loop_runs.status = 'cancelled'`
   directly — no `Decide` call needed, an operator-initiated hard stop). `ResolveEscalation(ctx,
   loopRunID string, override *EscalationOverride) (*LoopResult, error)` — if `override` is
   nil, calls `LoopEngine.Resume` normally (re-enters `Decide` with fresh state, letting the
   deterministic/reasoning logic decide again — useful if the escalation was e.g. "confirm
   before spending more budget" and the operator just wants to continue); if `override.ForceComplete`,
   persists `completed` status directly; if `override.ForceCancel`, same as `Cancel`; if
   `override.Replan` is set (a revised `definition_name` and/or goal fields), applies it
   then calls `Resume`.

2. **`internal/api/loops.go`** — REST surface, matching this codebase's existing handler
   conventions (check `internal/api/agent_schedules.go` or `internal/api/teams.go` for the
   current house style — request decode, validation, store call, response encode):
   - `POST /api/goals`, `GET /api/goals/{id}`, `GET /api/goals` (list, filterable by
     `status`/`parent_goal_id`), `PATCH /api/goals/{id}`, `DELETE /api/goals/{id}` — thin
     wrappers over task `01`'s CRUD.
   - `POST /api/loops` (body: `LoopLaunchRequest`) → `LoopLauncher.Launch`.
   - `GET /api/loops/{id}`, `GET /api/loops` (list, filterable by `goal_id`/`status`).
   - `POST /api/loops/{id}/cancel` → `LoopLauncher.Cancel`.
   - `POST /api/loops/{id}/resolve` (body: optional `EscalationOverride`) →
     `LoopLauncher.ResolveEscalation`.
   - `GET /api/loops/{id}/iterations` — thin wrapper over task `04`'s `ListLoopRunIterations`.
   - `GET /api/goals/{id}/evidence` — thin wrapper over task `02`'s `ListGoalEvidence`.

3. **Route registration** — `internal/api/api.go`, alongside the existing route blocks
   (`/api/teams`, `/api/agent-schedules`, etc.).

## Done means

- `LoopLauncher.Launch` end-to-end tested: inline-goal launch creates both the `Goal` and
  the `LoopRun`; existing-goal-id launch reuses the `Goal`; a second launch against a
  `goal_id` with an already-active `LoopRun` is rejected with a clear error.
- `ResolveEscalation` tested for all three override modes plus the no-override (resume
  normally) case, against a `LoopRun` genuinely parked in `waiting_on_escalation`.
- Every new endpoint round-trip tested against a real running server (not just unit tests on
  the handler function) per this codebase's live-dogfeed discipline for new REST surfaces.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
