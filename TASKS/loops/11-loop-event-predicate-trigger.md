# Event/predicate trigger — new `resume_loop_run` reflex action kind

**Phase:** 3 — Trigger surface (`TASKS/loops`)
**Status:** not-started
**Depends on:** `08-loop-engine-core.md`
**Touches:** `internal/agent/reflexes/` (new action kind), `internal/store/migrations/`
(new migration — widen whatever `CHECK` constrains `agent_reflexes.action_kind` or its
equivalent), wherever action-kind dispatch is wired (`Engine.EvaluateState` or the reflex
dispatch call sites `reflexes.Resolve`'s callers use).

## Context

Implements `docs/engineering/architecture/21-loops.md`'s "Event/predicate" trigger surface
— the design doc's own claim: *"the same reflex trigger-spec AST (`internal/agent/reflexes`)
Flex-step exit triggers already reuse, firing a `Resume` on a `WAIT`-status `LoopRun`."*

**This claim is incomplete — a real, load-bearing correction found this session, not a
minor nuance.** Confirmed by reading `internal/service/workflow_engine.go:150-169`'s own
comment on the flex-step precedent directly: flex's exit-trigger "reuse" is a **lazy
re-check** (`recheckFlexStep`) that only runs when *something else* already calls
`.Resume(ctx, runID, ...)` on the *outer* workflow for an unrelated reason — there is no
real push anywhere in the current codebase from "trigger fires" to "Resume gets called."
The one real external driver of a flex-waiting run's re-check today is A2A's `TaskManager`
task-poll path (`a2a_task_manager.go:744`). **A `WAIT`-status `LoopRun` has no A2A task
attached and nothing else calling its `.Resume` incidentally** — reusing the trigger-spec
*AST* (the predicate/event/interval evaluator, `internal/agent/reflexes/evaluator.go:15`'s
`EvaluateTrigger`) is correct and genuinely reusable; assuming the *firing mechanism* comes
along for free is not, and would ship a trigger surface that silently never fires.

**This planning session's resolution** (README, "Load-bearing corrections," item 2): a
real, new reflex action kind, not a generic `callback`. `docs/engineering/architecture/10-reflex-action-taxonomy.md:120`
(confirmed this session) explicitly rejected a generic 7th `callback` kind: *"an opaque
callback target is illegible to the combining-algorithm and provenance-ceiling facets... If
plugin-registered kinds are ever built, each one still gets its own row/identity/policy in
the real tables; `callback` is at most the underlying execution mechanism for such a row,
never a bare escape hatch of its own."* A named `resume_loop_run` action kind satisfies this
exactly — it's a legible, specific action with its own identity (a reflex row whose
`action_kind = 'resume_loop_run'` and whose config names a `loop_run_id`), evaluated by the
existing `reflexes.Resolve` combining algorithm (`internal/agent/reflexes/resolve.go:177`)
exactly like every other action kind, not a bypass of it.

**Real dispatch precedent to follow** — the design doc's own "New mechanism vs. reuse"
ledger already treats `dispatch_to_agent` as the template for "a reflex action kind that
does something beyond in-turn steering" (per `TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md`,
already landed). `resume_loop_run` is architecturally the same shape: a reflex fires, its
action kind's handler is invoked with the firing context, and that handler calls out to a
different subsystem (`LoopEngine.Resume`, task `08`) rather than affecting the current turn.

## What to do

1. **New migration** — widen whatever constrains `agent_reflexes`' action-kind values today
   (find the real mechanism — a DB `CHECK`, or a Go-side enum with no DB constraint;
   `dispatch_to_agent`'s own landed migration is the precedent to copy exactly, both for the
   DDL shape and for whichever it turns out to be). Re-verify the actual next-available
   migration number at dispatch time.

2. **`resume_loop_run` action kind** — register it wherever `dispatch_to_agent` and the
   other five kinds are registered (`internal/agent/reflexes`' action-kind registry/lookup —
   find the real file via the `ActionKindLookup` type `reflexes.Resolve`'s signature takes).
   Config shape: `{"loop_run_id": "<id>"}` (a reflex row scoped to resuming one specific
   `LoopRun` — created by task `08`/`10` when a `LoopRun` transitions to `WAIT` status with
   an event/predicate resume condition, not authored ad hoc by a human the way most reflexes
   are). Handler: on fire, call `LoopEngine.Resume(ctx, loopRunID)` (task `08`) — resolve
   the same potential import-cycle question task `09` already had to resolve for its own
   `internal/loop → internal/service` call direction, and reuse whatever resolution that
   task landed on (an injected notifier interface, most likely) rather than inventing a
   second pattern.

3. **Trigger-spec reuse (the part of the design doc's claim that IS correct)** — a
   `WAIT`-status `LoopRun`'s resume condition is authored using the existing predicate/event/
   interval trigger-spec vocabulary (`internal/agent/reflexes/evaluator.go`'s
   `EvaluateTrigger`), stored as this new reflex row's own `trigger_kind`/`trigger_spec`
   columns (`agent_reflexes` already has these — no new column needed here, only the new
   action kind). The reflex's own existing evaluation cadence (whatever currently calls
   `EvaluateTrigger` for other reflexes — `Engine.EvaluateState`'s per-turn pass, or a
   scheduled equivalent) is what actually drives this reflex's evaluation; confirm and
   document which real cadence applies to a `resume_loop_run` reflex, since it's not tied to
   any one session's per-turn loop the way most reflexes are (a `LoopRun`'s `WAIT` state can
   span far more than one chat turn) — if the existing per-turn `Engine.EvaluateState` path
   is genuinely the only evaluation cadence today, note explicitly whether that's sufficient
   for Loop's use case or whether task `12`'s scheduled `loop_run_tick` is actually the real
   evaluation cadence for this trigger kind (i.e., the "scheduled tick" trigger surface
   periodically re-evaluates any `resume_loop_run` reflexes attached to a still-`WAIT`ing
   `LoopRun`, making this task's own event/predicate surface effectively a specialization
   consumed by task `12`'s tick rather than a fully independent firing path) — this is a
   real architectural question this planning session leaves for the worker to resolve
   against the actual per-turn-vs-scheduled evaluation cadences in the code, with the
   resolution documented in the Work Log, not silently assumed.

## Done means

- `resume_loop_run` is a real, registered action kind, evaluated by `reflexes.Resolve`
  exactly like the other six.
- End-to-end test: a `LoopRun` parked in `WAIT` status with a `resume_loop_run` reflex
  attached (predicate or event trigger, your choice for the test), the trigger condition
  becomes true, and `LoopEngine.Resume` is genuinely called as a result — via whatever real
  evaluation cadence this task's own "What to do" item 3 investigation determined actually
  drives it, not a test that manually invokes the handler function directly.
- Explicit, documented resolution (Work Log) of the per-turn-vs-scheduled evaluation-cadence
  question in item 3.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
