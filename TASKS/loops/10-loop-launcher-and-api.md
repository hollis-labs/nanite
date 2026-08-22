# `LoopLauncher` + CRUD/launch/escalation-resolution API

**Phase:** 3 — Trigger surface (`TASKS/loops`)
**Status:** implemented
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

**Files touched:**
- `internal/loop/launcher.go` (new) — `LoopLauncher` (wraps `*LoopEngine` + `*store.Store`),
  `LoopLaunchRequest`, `GoalSpec` (alias of `LoopGoalSpec`), `EscalationOverride`,
  `ReplanOverride`, `ErrLoopRunNotWaitingOnEscalation`. `Launch`/`Cancel`/`ResolveEscalation`
  as specified.
- `internal/loop/launcher_test.go` (new) — 13 tests: inline-goal launch creates Goal+LoopRun,
  existing-goal-id launch reuses the Goal, second launch against an already-active goal is
  rejected (`ErrLoopRunAlreadyActive`), `Cancel` (found + unknown-id), and all four
  `ResolveEscalation` modes (no-override, empty-override, force_complete, force_cancel,
  replan) plus the not-waiting-on-escalation and unknown-id rejections — all against a
  LoopRun genuinely driven into `waiting_on_escalation` by a real `Run`/`Resume` call (real
  SQLite store, stub `StepExecutor`, no LLM).
- `internal/store/loop_runs.go` — added `UpdateLoopRunDefinitionName` (narrow updater,
  mirrors `BumpLoopRunIteration`/`UpdateLoopRunNoProgressStreak`). Not in the task's own
  "Touches" list, but mechanically required: `ResolveEscalation`'s replan override needs
  somewhere to persist a revised `definition_name` so `Resume`'s next iteration picks it up
  (`Resume` re-fetches `loop_runs` and passes `lr.DefinitionName` straight into
  `driveIterations`). No schema change — a new Go function over an existing column.
- `internal/api/loops.go` (new) — Goal CRUD (`POST/GET/PATCH/DELETE /api/goals`,
  `GET /api/goals/{id}/evidence`) as thin wrappers over task 01/02's store; Loop trigger
  surface (`POST /api/loops`, `GET /api/loops`, `GET /api/loops/{id}`,
  `POST /api/loops/{id}/cancel`, `POST /api/loops/{id}/resolve`,
  `GET /api/loops/{id}/iterations`) wrapping `LoopLauncher` and task 04's
  `ListLoopRunIterations`. Request/response DTOs documented inline (no OpenAPI doc, per
  "Done means").
- `internal/api/loops_test.go` (new) — 18 tests exercised through the real `mux.ServeHTTP`
  round trip (not calling handler functions directly): full Goal CRUD lifecycle, 404s,
  missing-intent rejection, evidence listing (+ 404 for an unknown goal), loop launch
  (inline goal, existing goal, 409 conflict, invalid JSON, launcher-not-wired 503), get/list
  loops + iterations (+ 404 for an unknown loop run), cancel (+ unknown-id 404), and all four
  resolve-escalation modes (+ 409 when not `waiting_on_escalation`, + 404 unknown id).
- `internal/api/api.go` — added the `loopLauncher *loop.LoopLauncher` field + `SetLoopLauncher`
  setter (mirrors `selfTools`/`SetSelfTools`'s post-construction-setter shape — not stored on
  `service.Container` itself since `internal/loop` imports `internal/service`, so a
  `*loop.LoopLauncher` field on `Container` would be an import cycle), plus route
  registration for the block above, placed alongside the existing `/api/teams` block.
- `cmd/nanite/main.go` — wires `a.SetLoopLauncher(loop.NewLoopLauncher(loopEngine,
  container.Store))` right after `a.SetSelfTools`, reusing the same `loopEngine` instance
  task 09's `StepKindLoop` support already constructed and wired onto `workflowEngine`.

**Design decisions / deviations from a literal reading of "What to do":**
- **Delegation, not duplication, applied to two checks, not just the one named explicitly.**
  The task's own Context names the one-active-LoopRun-per-goal check as the thing to delegate
  to `Run` rather than duplicate. The identical reasoning was applied to the GoalID/InlineGoal
  union check and to the inline-goal `CreateGoal` call: `LoopEngine.Run`'s own `resolveGoal`
  (task 08) already rejects an invalid GoalID/InlineGoal combination and already calls
  `store.CreateGoal` for the inline case. `LoopLauncher.Launch` forwards both fields straight
  into `LoopInput` and lets `Run` do all of that work, rather than re-implementing the union
  check or calling `CreateGoal` a second time in the launcher. The task's own prose ("if
  InlineGoal set, CreateGoal first; then call LoopEngine.Run") describes the *overall*
  behavior a caller observes, which delegating into `Run` achieves exactly — not a literal
  instruction that `Launch` itself must be the thing calling `CreateGoal`.
- **`Cancel` and `ResolveEscalation`'s `force_complete`/`force_cancel` branches call
  `LoopEngine`'s own unexported `notifyOuterOnTerminal`** (task 09's push mechanism for a
  `StepKindLoop`-launched LoopRun) after persisting a terminal status directly. Not explicitly
  named in this task's "What to do," but a real correctness gap otherwise: an
  operator-cancelled/force-completed LoopRun is exactly as terminal as one `Decide` reaches on
  its own, and skipping the notify would leave a `StepKindLoop` step's outer `WorkflowRun`
  stuck in `waiting_on_loop` forever. `launcher.go` lives in the same `internal/loop` package
  as `engine.go`, so this is a direct call, not a new exported surface.
- **`ResolveEscalation` checks `lr.Status == waiting_on_escalation` up front, uniformly,
  before any override branch** (including the no-override case, where `LoopEngine.Resume`
  would otherwise separately accept `waiting_on_gate` too). This is the endpoint's own
  documented precondition ("given a loop_run_id in *waiting_on_escalation* status") — `Resume`
  itself stays the broader, general-purpose primitive for task 11/12's own resume callers.
- **Replan's `DefinitionName` persistence required a new store method** not listed in this
  task's "Touches" — see the `internal/store/loop_runs.go` entry above. Judged in-scope
  ("pure Go... no schema migration" — a new function over an existing column is not a schema
  change) rather than an escalation, since without it the replan override's own documented
  behavior ("a revised `definition_name`... applies it then calls Resume") would have no way
  to actually take effect.
- **`EscalationOverride`/`ReplanOverride` carry their own `json` tags directly** (declared in
  `internal/loop`, not mirrored by a second API-layer DTO) since the wire shape needed no
  transformation from the engine-level type — unlike `LoopLaunchRequest`, which does need an
  API-layer DTO (`loopLaunchRequest`) because `GoalSpec`/`LoopResult`/`Decision` carry no json
  tags of their own (internal engine-input/output types, not meant for direct
  (de)serialization) and `LoopLaunchRequest.GoalID *string` needed a JSON-friendly mirror for
  `PATCH`-style semantics.

**Auth-model call (Context):** standard operator auth, no additional gating — confirmed
directly against `internal/server/server.go` (not just copied from Scheduling's own prose):
every `/api/*` route already passes through the fixed `recover -> logging -> CORS ->
basicAuthMiddleware -> callerIdentityMiddleware -> bodyLimitMiddleware` chain, applied
uniformly at the mux level. Matches `TASKS/scheduling/09-operator-http-api.md`'s own landed
call exactly, per this task's own instruction to mirror it rather than reinvent it.

**Live-dogfeed verification (real running server, not just Go-level tests):** built the real
`nanite` binary and ran it as a genuinely separate `nanite serve` process against a scratch
DB and a scratch `workflow_definitions_path` (a project-local `nanite.yaml` in a throwaway
working directory — confirmed via `git status --short` immediately afterward that no write
landed in the real tracked repo; the one file-backed write `POST /api/agents` triggers landed
under the scratch working directory's own `.nanite/agents/`, not the project's). Two trivial
`kind: tool` (no `llm` step, zero LLM/network dependency) `WorkflowDefinition`s were used,
each calling the real `tool_list` self-tool (an `always_included` known-tool escape-hatch
tool, confirmed via `internal/store/known_tools.go` and `internal/toolclient/broker.go`'s
`isToolGrantedToAgent`, so it's callable by any `agent_id` with zero manual grant setup) —
this let every endpoint be curled against a real server with a real `ToolService.Execute` →
MCP manager → self-tool round trip actually executing, with no Anthropic/OpenAI credentials
available in this environment. Round-tripped via curl, in order: `POST /api/agents` (real
agent profile), `POST/GET/GET(list)/PATCH/DELETE /api/goals`, `POST /api/loops` (existing-goal
and inline-goal launches, both reaching real `waiting_on_escalation` via
`budget.max_iterations=1`), `GET /api/loops/{id}`, `GET /api/loops?goal_id=`,
`GET /api/loops/{id}/iterations` (confirmed a real `workflow_run_id` + `progress` state per
iteration), `GET /api/goals/{id}/evidence`, `POST /api/loops/{id}/resolve` with no body
(resume advanced `current_iteration` 1→2, re-escalated), `{"force_complete":true}` (→
`completed`), `{"force_cancel":true}` (→ `cancelled`), and `{"replan":{"definition_name":...,
"desired_state":[...]}}` (persisted `definition_name` change confirmed via a follow-up
`GET`, goal's `desired_state_json` confirmed revised, and `Resume` launched a real second
iteration under the new definition), `POST /api/loops/{id}/cancel` standalone, and the error
paths: second launch against a goal with an active/paused LoopRun → 409
(`ErrLoopRunAlreadyActive`), resolve on an already-`completed` run → 409
(`ErrLoopRunNotWaitingOnEscalation`), `GET`/`resolve`/`cancel` on an unknown loop run id →
404, `POST /api/loops` with the launcher unwired → 503, malformed JSON body → 400. Scratch
directory removed after verification.

**Test status:**
- `go build ./cmd/nanite/` — clean.
- `go vet ./...` — reports the same two pre-existing `internal/service/container.go` findings
  (`stopReaper`/`stopRuntimeReaper` possibly-unused-on-some-paths) already present on `main`
  before this task's changes (confirmed via `git diff --stat internal/service/container.go`
  showing no changes from this task) — not introduced or touched here, matching
  `TASKS/scheduling/09`'s own identical finding for the same two functions.
- `go test ./internal/loop/... ./internal/api/...` — all pass, including the 13
  `TestLoopLauncher_*` and 18 `TestLoopsAPI_*` tests added by this task (verified by reading
  each test's own `--- PASS` line, not a masked exit code).
- `go test ./...` (whole repo) — run in full; verified by reading the real printed
  `ok`/`FAIL` lines per package rather than trusting `$?` alone, per this task's own
  instruction.

**A real bug this task's own test-writing caught (not a production bug):** two early draft
tests (`TestLoopLauncher_Launch_InlineGoal_CreatesGoalAndLoopRun`,
`TestLoopLauncher_Launch_ExistingGoalID_ReusesGoal`, and their `internal/api` mirrors) called
`Launch` with no budget override against a goal with no evidence ever recorded — a zero-value
`Budget{}` is documented (`engine.go`) as "no cap on any dimension, run until COMPLETE/FAIL,"
so with no evidence and a `StepExecutor` stub that always succeeds, `Decide` returns
`CONTINUE` forever and the test genuinely hung (confirmed as a real, sustained ~100%+ CPU busy
loop, not a slow-but-finite run, before being killed and diagnosed). Fixed by giving every
such test a bounded `Budget{MaxIterations: 1}` — this is expected, documented engine
behavior, not a bug in `LoopLauncher`/`LoopEngine` itself.

## Follow-up candidates
- 21-loops.md's own ledger already flags `loop_runs.status` collapsing WAIT/ESCALATE into one
  `waiting_on_escalation` bucket (task 08's own documented v1-scope note) — this task's REST
  surface inherits that same coarsening; a future `RunStatusWaitingOnLoop`-style status split
  would also need a corresponding split in this file's own status vocabulary/filters.
- No per-tool-call size/timeout guard is added around a Loop iteration's `tool` step beyond
  what `WorkflowLaunchRequest.TimeoutSeconds`/`ExecuteToolStep` already provide — not this
  task's scope, noted only because the live-dogfeed's own tool-only workflow shape is a real,
  reusable pattern for future Loop-related dogfeeding that needs zero LLM credentials.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
