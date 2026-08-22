# Event/predicate trigger — new `resume_loop_run` reflex action kind

**Phase:** 3 — Trigger surface (`TASKS/loops`)
**Status:** reviewed
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

**Migration number.** Used `142` exactly as the dispatch instruction required. Confirmed
`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -6` showed `141_workflow_run_waiting_on_loop_status.sql`
as the highest landed migration at dispatch time and no `142_*.sql` already present. File:
`internal/store/migrations/142_agent_reflex_resume_loop_run.sql`.

**Real correction found this session (item 3's own kind of finding, not a design reopening)
— "WAIT status" is the collapsed `waiting_on_escalation` literal, not a distinct one.**
Read `internal/loop/decide.go`/`engine.go` directly before writing anything: `Decide`'s
`DecisionWait` output (one of the 8 vocabulary values `docs/engineering/architecture/
21-loops.md` names) is **not** a separate `loop_runs.status` value. `evaluateDecideAndAct`'s
own `case DecisionWait, DecisionEscalate:` (engine.go ~L664) persists **both** to the exact
same `store.LoopRunStatusWaitingOnEscalation` — task 08's own comment there documents this
as a deliberate, already-known simplification ("loop_runs.status has exactly two pause
buckets... one fewer than decide.go's own WAIT/ESCALATE distinction... a future migration
could add a distinct waiting status for WAIT... not done here"). `LoopEngine.Resume`'s own
doc comment independently confirms this task's intended shape was anticipated at task-08
authorship time: "Called by task 10's escalation-resolution endpoint, task 11's reflex-fired
resume, and task 12's scheduled tick alike — all three go through this one method." Given
this, "a WAIT-status LoopRun" throughout this task is realized as a LoopRun whose
`loop_runs.status = 'waiting_on_escalation'` — not a distinct DB literal. This does not
change what this task builds (a real `resume_loop_run` reflex + handler that calls
`LoopEngine.Resume` on such a LoopRun); it only corrects which literal to check/target. Noted
here per this project's "note the correction, do the task anyway" policy — no schema change
to add a third `loop_runs.status` bucket was in this task's scope (that remains the
documented future follow-up task 08 already named), and my `EvaluateLoopRunResumeReflexes`
guards on `LoopRunStatusWaitingOnEscalation` specifically (not `LoopRunStatusWaitingOnGate`,
which is the *other* pause bucket and means something structurally different — the current
iteration's own inner WorkflowRun is blocked on a gate/flex step, which an event/predicate
`resume_loop_run` reflex was never meant to resolve).

**1. New migration (`142_agent_reflex_resume_loop_run.sql`).** Confirmed the real mechanism
first: `agent_reflexes.action_kind` is a `TEXT` column with both a `CHECK (action_kind IN
(...))` *and* a `REFERENCES reflex_action_kinds(name)` FK (added together by migration
`124_reflex_action_taxonomy.sql`) — `119_agent_reflex_dispatch_to_agent.sql` is the
pre-taxonomy precedent for the CHECK alone; `124` is the exact shape to copy for both. Also
confirmed `agent_reflexes` gained a further column since 124/131 that a rebuild must now
carry: `workflow_run_id` (migration `131_agent_reflexes_workflow_run_scoping.sql`, a plain
`ALTER TABLE ADD COLUMN`, no rebuild needed at the time) — this is the *first* rebuild of
this table since that column was added, so `142`'s `CREATE TABLE agent_reflexes_new` is the
first to include it. Same `PRAGMA foreign_keys=OFF` / rename-recreate-copy pattern as
119/124/131 for the table itself, plus new seed rows: `reflex_action_kinds` gets
`('resume_loop_run', 'execute_action', 'all_applicable', 0)` and
`reflex_action_kind_provenance_allow` (migration 125's table) gets `resume_loop_run` ×
`{system, operator}` (not `plugin`, mirroring `halt_session`/`dispatch_to_agent`'s own
restriction — a real subsystem side effect, not an advisory nudge). `combining_algorithm`
choice (`all_applicable`, not `dispatch_to_agent`'s `first_applicable`): resuming the same
LoopRun more than once in one pass (whether from one or several candidates) is
idempotent-safe per `LoopEngine.Resume`'s own already-tested "a second Resume against the
still-waiting LoopRun must behave identically" property — unlike routing one turn to two
different agents, there is no real conflict to arbitrate. `default_recurrence_seconds=0`
(no cooldown): mirrors `dispatch_to_agent`'s own reasoning — the real evaluation cadence
(a scheduled tick) needs every invocation to genuinely re-check the trigger. Verified against
a real backup copy (`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`,
copied into a scratch path first): every real pre-existing `agent_reflexes` row (14+ rows)
survives the rebuild with its `action_kind` intact, and a genuine `resume_loop_run` row can
be inserted against the migrated copy — `TestRealBackupAgentReflexesSurviveResumeLoopRunMigration`
(`internal/store/migration_142_agent_reflex_resume_loop_run_test.go`). Also added a
`goose DownTo(141)` + raw-SQL-probe-row + `Up()` replay test
(`TestMigrate142PreservesExistingRowsAcrossRebuild`, same technique as 124's own
`TestMigrate124BackfillsProvenanceTierFromCreatedBy`) confirming a pre-142 row survives
unchanged and that `resume_loop_run` is genuinely rejected pre-142 / accepted post-142.
**Collateral fix, mechanical, not a design question** (same class as task 09's own
migration-136 collateral fix): `migration_124_reflex_action_taxonomy_test.go` and
`migration_125_reflex_action_kind_provenance_allow_test.go` both hardcode exact row counts
(6 kinds, 16 allowed pairs) that these tests check against the *fully-migrated* live schema,
not migration 124/125 in isolation — bumped to 7/18 (and added `resume_loop_run` to both
tests' expectation maps) since my new migration is real data those two pre-existing tests
now also see.

**2. `resume_loop_run` action kind + handler.** `store.ReflexActionResumeLoopRun =
"resume_loop_run"` constant (`internal/store/agent_reflexes.go`), config shape
`{"loop_run_id": "<id>"}` exactly as specified. Registered in `internal/api/reflexes.go`'s
`validateReflexDefinition` action-kind switch (so the CRUD/PATCH/dry-run-validate API doesn't
permanently reject a legitimate `resume_loop_run` row) plus a spec-shape check requiring a
non-empty `loop_run_id`, mirroring `dispatch_to_agent`'s own `agent_slug` check immediately
above it. New read primitive: `Store.ListAgentReflexesForLoopRun(ctx, loopRunID)` — scoped via
`json_extract(action_spec, '$.loop_run_id') = ?` (SQLite's built-in JSON functions, core since
3.38, well below `modernc.org/sqlite`'s bundled version — confirmed working via this task's
own new tests, no prior query in this file needed `json_extract` before). Not a new
`agent_reflexes` column (`workflow_run_id`'s FK targets `workflow_runs(id)`, and `LoopRun` is
a peer entity to `WorkflowRun`, per `21-loops.md`'s Decision 1 — that column genuinely does
not fit a `loop_runs.id` value), exactly as the task's own item 3 already settled ("no new
column is needed... only the new action kind").

**Import-cycle resolution — reused task 09's exact "consumer declares narrow interface"
pattern, both directions verified directly, not assumed.** Confirmed via `grep`:
`internal/loop` already imports `internal/service` (task 08's `WorkflowLauncher`), and
`internal/service` already imports `internal/agent/reflexes` (extensively — `chat_reflexes.go`,
`chat_reflex_dispatch.go`, `container.go`, etc.), but `internal/service` does **not** import
`internal/loop` anywhere outside `cmd/nanite/main.go`'s wiring. This means a *third* package
in the cycle chain than task 09 dealt with is actually the load-bearing one here: my handler
needs to run inside `internal/agent/reflexes`' `Executor.Apply` conceptually, but
`internal/agent/reflexes -> internal/loop` would create
`reflexes -> loop -> service -> reflexes`, a real cycle (since `internal/loop` imports
`internal/service`, which imports `internal/agent/reflexes`). Resolved with the exact same
shape task 09 used for its own "launch direction" fix (`LoopStepLauncher`):
  - `Executor.Apply`'s `resume_loop_run` case (`internal/agent/reflexes/executor.go`) is a
    documented no-op — same shape and same underlying reason as `ReflexActionDispatchToAgent`
    immediately above it (the real effect needs a subsystem this package doesn't depend on).
  - `internal/agent/reflexes/engine.go`'s `EvaluateState` filters `resume_loop_run` out of its
    candidate list before `Resolve()` ever sees it — for the identical, already-documented
    Phase-4-item-09 reason `dispatch_to_agent` is filtered (a no-op `Apply()` would otherwise
    get treated as a genuine fire: bumped `fired_count`, a redundant `event_log` write, false
    plugin-hook signal).
  - The real handler + evaluation entry point is a new, dedicated call site —
    `internal/service/loop_resume_reflex.go`'s `EvaluateLoopRunResumeReflexes` — mirroring
    `attemptReflexDispatch` (`chat_reflex_dispatch.go`)'s own shape exactly: lists candidates
    (`Store.ListAgentReflexesForLoopRun`), calls the shared `reflexes.Resolve()` primitive,
    applies the Facet-4 cooldown cascade explicitly (kind-level default fetched via
    `Store.GetReflexActionKind`), emits unified telemetry via `reflexes.EmitFirings` (the
    post-taxonomy unified sink every real caller now goes through), and — for whichever
    candidate(s) `Resolve()` selects — calls a `LoopRunResumer` directly.
  - `LoopRunResumer` (one method, `ResumeLoopRun(ctx, loopRunID) error`) is declared in
    `internal/service` (the consumer), not a direct `internal/loop` import — legal for
    `internal/service` to declare without importing `internal/loop` at all (only
    `cmd/nanite/main.go` needs to reference both packages together, exactly the existing
    `LoopStepLauncher`/`OuterResumeNotifier` wiring shape).
  - `*loop.LoopEngine` satisfies `LoopRunResumer` structurally via a new, thin
    `ResumeLoopRun` method (`internal/loop/reflex_resume.go`) that calls the real `Resume`
    and discards its `internal/loop`-native `LoopResult` — legal there because `internal/loop`
    already imports `internal/service`. Added the compile-time assertion
    (`var _ service.LoopRunResumer = (*LoopEngine)(nil)`) proactively this time, since task 09's
    own Work Log flagged the *lack* of the symmetric assertion on `OuterResumeNotifier` as a
    real (if minor) review-found gap.
  - Guard added beyond the bare interface: `EvaluateLoopRunResumeReflexes` only proceeds when
    the target LoopRun's live `loop_runs.status` is currently `waiting_on_escalation` — a
    `waiting_on_gate` LoopRun is blocked on its *current iteration's own inner* WorkflowRun
    (a gate/flex step inside it), which an event/predicate `resume_loop_run` reflex was never
    meant to resolve (calling `Resume` there would still "work" mechanically — `Resume`
    dispatches to `resumeBlockedIteration` for that status — but would be resuming the wrong
    thing for the wrong reason). Any other status (already resumed, terminal, or a genuine
    race against a concurrent resolution) is a harmless no-op, not an error.

**3. Trigger-spec reuse + evaluation-cadence resolution (the genuine open question).**
Confirmed `internal/agent/reflexes/evaluator.go`'s `EvaluateTrigger` needed **zero** changes —
the predicate/event/interval AST is consumed exactly as-is by
`EvaluateLoopRunResumeReflexes` via the shared `reflexes.Resolve()` call, and
`agent_reflexes.trigger_kind`/`trigger_spec` (already-existing columns) are reused unchanged
for a `resume_loop_run` row, confirming that half of the design doc's claim is correct as
written.

The firing-mechanism half is resolved as follows, investigated against the real code rather
than assumed: **`Engine.EvaluateState`'s per-turn pass is structurally excluded, not merely
insufficient, for `resume_loop_run` — the sole real cadence is whatever explicitly calls
`EvaluateLoopRunResumeReflexes`, which in this codebase's actual design is task 12's scheduled
`loop_run_tick`.** Two independent reasons converge on this, not one:
  1. **Structural exclusion (same class as `dispatch_to_agent`).** `resume_loop_run` is
     filtered out of `EvaluateState`'s own candidate list (see above) for the identical reason
     `dispatch_to_agent` already is — its `Executor.Apply` case is a no-op, so letting it
     reach `Resolve()` inside `EvaluateState` would corrupt telemetry (false fired_count bump,
     no dispatch/resume having actually occurred). This alone rules out `EvaluateState` as a
     viable cadence for this kind, independent of any session-liveness question.
  2. **No live session to piggyback on, even setting (1) aside.** Read
     `internal/agent/reflexes/state.go`'s `StateCollector.Collect` directly: it is fundamentally
     session-scoped — every field (`Messages`, `UserMessages`, `MailUnreadCount`, `Events`,
     `TickN`) is built from a `sessionID`-keyed query (`LastNAssistantMessages`,
     `recentMessagesByRole`, `agent_messages.to_session_id`, `event_log.session_id`,
     `sessions.message_count`). `loop_runs` (migration `138_loop_runs.sql`) has no
     `session_id` column at all — a `LoopRun` is a peer entity to `WorkflowRun`, not itself a
     chat session, and its `waiting_on_escalation` pause can span (per the design doc's own
     framing) far more than one chat turn, up to and including zero live chat turns for the
     entire pause (a headlessly-launched loop, or one whose launching session ended long
     before the external condition becomes true). Even if `resume_loop_run` were *not*
     filtered out of `EvaluateState`, there would be no live, recurring per-turn pass for a
     `LoopRun` with no attached session to invoke it from in the first place.

Both facts together mean the per-turn path isn't "insufficient but usable as one input among
several" — it is not a candidate cadence for this kind at all. Per
`docs/engineering/architecture/21-loops.md`'s own "Trigger surface" section (`loop_run_tick`:
"a new `loop_run_tick` `JobType`... for... a WAIT-status loop polling an external condition")
and this task's own dispatch brief, the real, sole cadence is task 12's scheduled tick calling
`EvaluateLoopRunResumeReflexes` (or an equivalent wrapper) against each still-`waiting_on_escalation`
LoopRun it's responsible for. This confirms, rather than merely restates, the design doc's own
suspicion: the "Event/predicate" trigger surface's *AST* is fully reused, but its *firing
mechanism* for this specific action kind is a specialization consumed by the scheduled-tick
surface, not an independently-sufficient cadence of its own. Documented in
`internal/service/loop_resume_reflex.go`'s own package doc comment as the load-bearing design
note future readers of that file need, not only here.

**End-to-end test (Done-means, not reduced to a direct handler call).**
`internal/loop/reflex_resume_test.go`'s
`TestResumeLoopRunReflex_FiresLoopEngineResume_ViaRealEvaluationCadence`: a real `*LoopEngine`
(real `*store.Store`, real `BuiltinWorkflowEngine`/`WorkflowLauncher`, only the leaf
`StepExecutor` stubbed — same convention `engine_test.go` already uses) drives a real `Run`
with `Budget{MaxIterations: 2}` to a genuine `waiting_on_escalation` pause (the exact,
already-proven path `TestLoopEngine_Run_BudgetExhausted_EscalatesThenResumeContinues` uses).
A real `resume_loop_run` reflex row (event trigger, `{"name":"external_check_passed"}`) is
attached via `store.InsertAgentReflex` (authoring such a row is task 08/10's job per this
task's own Context, not this task's — the test seeds it directly, the same way every other
reflex-fixture test in this codebase seeds a candidate row). The test then calls
`service.EvaluateLoopRunResumeReflexes` — never `eng.Resume`/`eng.ResumeLoopRun` directly —
first with the event absent (asserts `fired=false` **and** that the LoopRun's independently
re-fetched `status`/`current_iteration` did not move), then with the event present (asserts
`fired=true` **and** that the LoopRun genuinely advanced to a real further iteration that
records qualifying goal evidence and completes: `status=completed`, `current_iteration=3`,
the fake LLM step executor's call count reaching 3). This is real forward `LoopEngine.Resume`
execution observed as a side effect of the evaluation call, not a mocked/stubbed resume path.
Unit-level coverage of `EvaluateLoopRunResumeReflexes` itself (trigger-false no-op,
`waiting_on_escalation`-status guard, resumer-error surfacing, no-candidates no-op, argument
guards) lives in `internal/service/loop_resume_reflex_test.go` with a `fakeLoopRunResumer`
test double (that package cannot import `internal/loop` for a real one — that's the entire
reason `LoopRunResumer` is an interface).

**Verification.** `go build ./cmd/nanite/`: exit 0, empty output. `go vet ./...`: exit 1, but
the only reported issues are the same pre-existing `internal/service/container.go` lostcancel
warnings (lines 1180/1200/1260) task 09's own Work Log already confirmed as pre-existing and
unrelated to loops work (a file this task never touched). `go test -count=1 ./...` (after
`go clean -testcache`): exit 0, zero `FAIL` lines across all 106 packages (all real command
output read directly via the `Read` tool from a redirected log file, never through a
piped/masked exit code, per this task's own instruction).

**No deviation from the task's core "What to do" items.** All three items (migration, action
kind + handler, trigger-spec reuse + cadence documentation) are implemented exactly as scoped.
The one genuine correction found and carried through (item above: "WAIT status" realized as
the collapsed `waiting_on_escalation` literal, not a distinct one) does not change what was
built, only which literal `EvaluateLoopRunResumeReflexes` checks — noted per this project's
"note the correction, do the task anyway" policy, not escalated.

**2026-08-21/22 integration fix (post-merge, not a re-opening of this task's own scope).**
`TASKS/ESCALATIONS.md`'s 2026-08-21 entry ("Phase 3's two parallel trigger tasks (`11`, `12`)
built compatible but disconnected mechanisms") confirmed what this task's own item-3
investigation above already predicted structurally but couldn't verify against task `12`'s
code (concurrent, isolated worktree): task `12`'s `enqueueLoopRunTick` wired
`RunnerAdapter.Loops` directly to `*LoopEngine`'s bare `Resume`, never calling
`EvaluateLoopRunResumeReflexes` — this task's entire `resume_loop_run` reflex path was real,
correct, fully unit-tested, and completely unreachable from any production caller. See task
`12`'s own Work Log below for the fix from that task's side; from this task's side, the fix
changed `EvaluateLoopRunResumeReflexes`'s return signature from `(fired bool, err error)` to
`(fired bool, hadCandidates bool, err error)` (`internal/service/loop_resume_reflex.go`) so a
caller can distinguish "no `resume_loop_run` reflex attached at all" from "one is attached,
its trigger just hasn't fired yet" — a distinction the original `(false, nil)`-for-both shape
could not make, and the exact gap that let the wiring bug go undetected. All of this task's
own test call sites (`loop_resume_reflex_test.go`, `reflex_resume_test.go`) were updated for
the new 3-value return; no existing test's assertions changed in substance, only the added
`hadCandidates` check per case. The real bridging fix — `internal/loop/tick_resume.go`'s
`TickResumeBridge`, now what `cmd/nanite/main.go` wires as `RunnerAdapter.Loops` — and its
new end-to-end test (`internal/loop/tick_resume_test.go`) live in task `12`'s file/side of
this shared fix; both task files cross-reference each other. Re-verified after the fix:
`go build ./cmd/nanite/` exit 0, `go vet ./...` exit 1 with only the same pre-existing
`container.go` lostcancel warnings noted above (confirmed via `git blame` to predate this fix
by months, commits `76df826a3`/`7a0e37936`), `go test ./...` exit 0 across all packages
(direct log read, not a piped/masked exit code).

## Review notes

PASS. Reviewed migration 142, the `engine.go`/`executor.go` diffs, `api/reflexes.go`'s
validation diff, `store/agent_reflexes.go`'s `ListAgentReflexesForLoopRun`,
`loop_resume_reflex.go`, `reflex_resume.go`, and both test files in full. Confirmed the
no-op/real-handler split genuinely mirrors `dispatch_to_agent`, the `waiting_on_escalation`
guard is correct and matches `LoopEngine.Resume`'s real branching (`waiting_on_gate` routes to
`resumeBlockedIteration`, a different path — verified directly, not just asserted), Facet 4
recurrence and `EmitFirings` telemetry are correctly wired, provenance tier correctly denies
`plugin`, and the end-to-end test genuinely drives `LoopEngine.Resume` via the reflex path
(never a direct call standing in for it). No `callback`-style escape hatch introduced. Import-
cycle resolution independently re-verified (`internal/loop`→`internal/service`→
`internal/agent/reflexes`, no reverse edge). `go build`/`go vet`/`go test` all verified
directly (vet's only output is the pre-existing `container.go` lostcancel warnings; full
`go test -count=1 ./...` after `go clean -testcache`: zero `FAIL`/`panic`).

One minor, non-blocking observation: no dedicated API-level test rejects an empty
`loop_run_id` for `resume_loop_run` — but this matches the equally-untested pre-existing
`dispatch_to_agent`/`agent_slug` precedent, not a new gap this task introduced.

The known `11`/`12` scheduled-tick wiring gap (see `TASKS/ESCALATIONS.md`'s most recent entry)
is tracked and being fixed separately — it reflects a cross-task integration seam neither
task's own file anticipated, not a defect in this task's own implementation, which is complete
and correct on its own terms.

**2026-08-22 addendum**: the 11/12 integration fix (`internal/loop/tick_resume.go`'s
`TickResumeBridge`, and the `EvaluateLoopRunResumeReflexes` signature change to
`(fired, hadCandidates, err)`) has landed and been independently re-reviewed. Confirmed sound:
no double-resume when a reflex fires, no blind-resume over a reflex whose trigger hasn't fired
yet (the exact bug), real end-to-end test coverage, no import cycle, a single shared
`ReflexEngine` instance. `go build`/`go vet`/`go test` all verified with real exit codes. This
task's own `resume_loop_run` mechanism is now genuinely reachable in production, not just
correct in isolation.
