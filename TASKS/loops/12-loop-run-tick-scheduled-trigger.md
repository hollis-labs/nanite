# Scheduled trigger — new `loop_run_tick` `JobType`

**Phase:** 3 — Trigger surface (`TASKS/loops`)
**Status:** reviewed
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

**Items 1-4 (dispatch plumbing), built exactly per this task's own spec, no deviation.**
`internal/scheduler/runner_adapter.go`: `JobTypeLoopRunTick = "loop_run_tick"` alongside the
existing four consts; `LoopRunTickPayload{LoopRunID string}` exactly as specified; a new
`LoopResumer` narrow interface (`Resume(ctx, loopRunID string) (loop.LoopResult, error)` —
confirmed the real signature directly against `internal/loop/engine.go` before writing this,
per the task's own instruction: it returns `(LoopResult, error)`, not the design doc's bare
`error` paraphrase) and a new `LoopRunLookup func(ctx, id string) (*store.LoopRun, error)`
func type (mirrors `ReflexLookup`'s own precedent for "a lookup func plus the real dispatch
dependency" — needed because, unlike the other four job types, `LoopResumer.Resume` is NOT
safe to call unconditionally: `LoopEngine.Resume`'s own switch hard-errors for any
`loop_runs.status` other than `waiting_on_gate`/`waiting_on_escalation`, so the no-op guard
this task's own Context recommends has to check status *before* calling Resume). New
`Loops`/`LoopRunLookup` fields on `RunnerAdapter`, new `case JobTypeLoopRunTick:` in
`Enqueue`'s switch, new `enqueueLoopRunTick` method: decodes the payload, looks up the
`LoopRun`, treats any non-`waiting_on_gate`/`waiting_on_escalation` status (including
`running`, per the task's own "not actually WAITing" wording) as a cheap no-op, otherwise
calls `Resume`. `internal/scheduler/store_adapter.go`: `JobTypeLoopRunTick` added to
`buildPayload`'s existing pass-through case (identical treatment to
`agent_workflow_run`/`command_run`/`reflex_dispatch` — no producer-specific resolution step
needed, unlike `durable_agent_wake`'s instance-id resolution). Migration `143` (assigned
number, confirmed via `ls internal/store/migrations/ | sort -t_ -k1 -n | tail` that no
`143_*.sql` existed in this worktree before writing it) widens `agent_schedules.job_type`'s
CHECK from four values to five, via the identical rename-recreate-copy pattern migration 127
itself used (SQLite can't `ALTER` a `CHECK` in place). **Tested against a real backup DB, not
just an empty fixture**: copied
`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`
(a genuinely old snapshot — pre-goose, pre-127, no `job_type` column at all) into the
scratchpad, ran the real `store.New(ctx, path)` migrate path against the copy via a throwaway
test in `internal/store` (`TestScratchMigrateRealBackup`, deleted after use, never committed):
it cut over cleanly through the legacy-ledger seeding path and applied every migration through
143, and a real `INSERT` of a `job_type='loop_run_tick'` row against the resulting schema
succeeded. Confirmed `internal/store/agent_schedules.go`'s `ScheduleJobTypeDurableAgentWake`
et al. convention (a second, independently-declared job-type constant set, not shared with
`internal/scheduler`'s) and added `ScheduleJobTypeLoopRunTick` there to match, matching the
existing (if duplicative) two-independent-copies discipline this codebase already uses for
this exact string across the two packages, and used it as `LoopRunTickPayload`'s `JobType`
value in `internal/loop/tick_schedule.go` (below) without creating a
`internal/loop -> internal/scheduler -> internal/loop` cycle — confirmed via a clean
`go build ./internal/scheduler/... ./internal/loop/...` immediately after `internal/scheduler`
started importing `internal/loop` for `LoopResumer`'s `loop.LoopResult` return type (no cycle:
neither `internal/store`, `internal/service`, `internal/agentworkflow`, nor
`internal/agent/reflexes` — everything `internal/scheduler` already imported — imports
`internal/loop`, confirmed by grep before making the change).

**Item 5 — real investigation, resolved by building the minimal creator, not left
unaddressed.** Checked task 08's landed Work Log and its actual code
(`internal/loop/engine.go`, `reviewed`) directly: it never calls
`store.InsertAgentSchedule` anywhere (grepped the whole package). Task 10
(`TASKS/loops/10-loop-launcher-and-api.md`) was still `not-started` at dispatch time (running
concurrently in a separate, isolated worktree this task cannot see) and its own "What to do"
only names `Launch`/`Cancel`/`ResolveEscalation` — no automatic `agent_schedules` producer
named there either. So neither party builds this. **Built the minimal creator in a new file,
`internal/loop/tick_schedule.go`**, wired from one call site: `evaluateDecideAndAct`'s
`DecisionWait` branch (`internal/loop/engine.go`) — the one, unambiguous moment
`21-loops.md`'s trigger-surface bullet names verbatim ("a WAIT-status loop polling an
external condition"). `DecisionEscalate` (the other half of the same collapsed
`LoopRunStatusWaitingOnEscalation` bucket, per task 08's own documented WAIT/ESCALATE
collapse) deliberately does NOT get a tick scheduled — it means "needs a human," resolved via
task 10's future `ResolveEscalation` endpoint or a direct `Resume` call, not an automatic
re-poll; scheduling one there would be actively wrong (silently resuming a `LoopRun` an
operator hasn't looked at yet). **Real correction to the design doc's own framing, found and
documented rather than worked around silently**: `21-loops.md` names two triggers for this
job type — "a 'durable'-preset loop OR a WAIT-status loop polling an external condition" — but
no preset registry or continuation-policy/budget field distinguishing "this WAIT is a
durable-preset poll" from any other kind of external wait exists anywhere in the codebase yet
(confirmed directly: `TASKS/loops/13-loop-presets.md`, the task that would define "durable,"
is still `not-started`; `ContinuationPolicy`/`Budget`, both fully read, carry no
polling-cadence or preset-marker field). Rather than invent preset infrastructure this task
was never scoped to build, every `DecisionWait` — not just a hypothetical "durable preset"
subset — is treated as the polling case; documented at length in `tick_schedule.go`'s own
package doc comment as a real follow-up candidate for task 13 to narrow once a genuine
polling-cadence marker exists. A placeholder `defaultLoopRunTickPollInterval = 5 * time.Minute`
constant stands in for that not-yet-existing per-preset cadence field, also documented as a
follow-up candidate. The schedule row's id is deterministic
(`loopRunTickScheduleID(loopRunID) = "loop_run_tick:" + loopRunID`, not a fresh UUID per call)
specifically so `InsertAgentSchedule`'s own `INSERT OR REPLACE` naturally dedups a `LoopRun`
that cycles through WAIT more than once, rather than accumulating one schedule row per WAIT
episode — tested directly
(`TestScheduleLoopRunTick_DeterministicID_ReplacesNotDuplicates`). `agent_id` (a
`NOT NULL REFERENCES agent_profiles(id)` column) is sourced from
`loopRunPersistentConfig.AgentProfileID` (task 08's own placeholder shape for
`loop_runs.continuation_policy_json`), decoded locally in `engine.go`'s `DecisionWait`
branch via the existing unexported `decodeLoopRunPersistentConfig` — no signature change to
`evaluateDecideAndAct` needed. A scheduling failure here is surfaced as a real, propagated
error (not silently swallowed), matching this file's own established "surface failures, don't
mask them" convention (`parseReasoningVerdict`'s own doc comment makes the same call).
**Documented, not solved, known limitation**: this inserts exactly one one-shot tick per WAIT
decision; if that tick fires and the condition still isn't met, nothing re-schedules a second
one automatically (no self-rescheduling poll loop exists yet) — left as a real follow-up
candidate for task 13's preset work, the party best positioned to own a genuine recurring
polling-cadence contract.

**Wiring**: `cmd/nanite/main.go`'s existing `scheduleRunnerAdapter := &scheduler.RunnerAdapter{...}`
literal (constructed at the point 03/04/05/06's own dependencies are already wired) now also
sets `Loops: loopEngine` and `LoopRunLookup: s.GetLoopRun` — `loopEngine` was already
constructed several lines above (task 09's `StepKindLoop` wiring) and already satisfies
`LoopResumer` directly; `s.GetLoopRun` already satisfies `LoopRunLookup`'s func-type shape.
Nothing new to build at the wiring layer, unlike `reflex_dispatch`'s still-documented gap
(`ReflexLookup`/`ReflexExecutor` deliberately left unconfigured in `main.go` — a real,
already-established precedent in this exact file for "wire the dispatch type even though its
one real producer doesn't exist yet," which item 5's resolution here now closes for
`loop_run_tick` specifically).

**Tests** (matching the existing `runner_adapter_test.go`/`store_adapter_test.go` fake-based
patterns exactly, not inventing a new one): `fakeLoopResumer`/`fakeLoopRunLookupFor` added
alongside the file's existing fakes.
`TestEnqueue_LoopRunTick_WaitingOnEscalation_ResumesLoop`,
`TestEnqueue_LoopRunTick_WaitingOnGate_ResumesLoop`,
`TestEnqueue_LoopRunTick_NotResumableStatusIsNotAnError` (table-driven over
running/completed/failed/cancelled, asserting `Resume` is never called — the no-op guard),
`TestEnqueue_LoopRunTick_UnknownLoopRunIsAnError`, `TestEnqueue_LoopRunTick_MissingLoopRunID`;
extended `TestEnqueue_NotConfiguredReturnsClearError` and
`TestEnqueue_MalformedPayload_ReturnsErrorNotPanic` with the fifth job type.
`store_adapter_test.go`: extended `TestStoreAdapter_ListDueSchedules_PassThroughJobPayload`
and `TestStoreAdapter_Payload_CompatibleWithRunnerAdapter_AllJobTypes` with a `loop_run_tick`
row (the latter proves a real `gosched.Job` built from `StoreAdapter`'s own output dispatches
correctly through `RunnerAdapter.Enqueue`, calling `Resume`). `internal/loop/tick_schedule_test.go`
(new): direct unit coverage for `scheduleLoopRunTick` (`TestScheduleLoopRunTick_InsertsRow`,
`TestScheduleLoopRunTick_DeterministicID_ReplacesNotDuplicates`,
`TestScheduleLoopRunTick_MissingAgentProfileID_ReturnsError`), plus two integration-style
tests exercising `evaluateDecideAndAct`'s real call site directly
(`TestEvaluateDecideAndAct_DecisionWait_SchedulesLoopRunTick`,
`TestEvaluateDecideAndAct_DecisionEscalate_DoesNotScheduleLoopRunTick`) — calling the
unexported `evaluateDecideAndAct` directly (same package, `package loop`) rather than driving
a full `Run()`/`Resume()` call through a real no-progress-triggering `WorkflowDefinition`
mirrors this same test file's own established precedent
(`TestLoopEngine_DriveIterations_CallerContextDead_DoesNotEscalateOrFailBudget`) for testing
an internal engine method directly when engineering the realistic public-API path to a given
scenario is substantially more complex than the one behavior actually under test warrants. A
real `WorkflowRun` is still launched first (via the package's own unexported `e.launcher`) so
`iterRow`/`launchResult` carry a genuine, FK-valid `workflow_run_id`; only `StepResults` is
then hand-crafted (one passed + one failed verify) to force `classifyIterationProgress`'s
`NO_PROGRESS` branch, driving `Decide`'s reasoning fallback (`fakeStepExecutor.llmFunc`
distinguishes the reasoning-fallback call from the iteration's own ordinary LLM step call via
`req.SystemPrompt == reasoningSystemPrompt`, the same discriminator `decide.go` itself sets).

**Verification (all read directly from un-piped, file-redirected output; exit codes checked
immediately, and never trusted over the actual printed text, per this batch's own standing
instruction)**: `go build ./cmd/nanite/` — exit 0, no output. `go vet ./...` — exit 1, but the
four findings are the exact same pre-existing, unrelated `internal/service/container.go`
possible-context-leak findings task 08's own Work Log already documented; confirmed via
`git status`/`git diff --stat` that `container.go` is untouched by this task (only
`cmd/nanite/main.go`, `internal/loop/engine.go`, `internal/scheduler/runner_adapter.go`,
`internal/scheduler/runner_adapter_test.go`, `internal/scheduler/store_adapter.go`,
`internal/scheduler/store_adapter_test.go`, `internal/store/agent_schedules.go` changed, plus
new files `internal/loop/tick_schedule.go`, `internal/loop/tick_schedule_test.go`,
`internal/store/migrations/143_agent_schedules_loop_run_tick_job_type.sql`). `go test ./...`
— read the full, real output directly (not the shell's own final exit code, which reflected a
trailing `grep -E "^(FAIL|--- FAIL)"` finding zero matches — grep's own documented exit-1-on-
no-match behavior, not a test failure): every one of the 105 listed packages printed `ok` or
`[no test files]`, zero `FAIL` lines anywhere, including
`ok github.com/hollis-labs/nanite/internal/loop 13.684s` and
`ok github.com/hollis-labs/nanite/internal/scheduler 28.118s` (both freshly run, not cached).

**2026-08-21/22 integration fix (post-merge, not a re-opening of this task's own scope).**
`TASKS/ESCALATIONS.md`'s 2026-08-21 entry ("Phase 3's two parallel trigger tasks (`11`, `12`)
built compatible but disconnected mechanisms"): this task's `enqueueLoopRunTick` (unchanged by
this fix — it already did the right thing, checking status then calling `r.Loops.Resume`) was
wired to `RunnerAdapter.Loops = loopEngine` in `cmd/nanite/main.go` — a bare `*LoopEngine`
whose `Resume` blind-resumes unconditionally, never consulting task `11`'s `resume_loop_run`
reflex evaluation entry point (`service.EvaluateLoopRunResumeReflexes`). Built genuinely in
parallel, isolated worktrees, this task's own file had no way to see task `11`'s final reflex
mechanism, and task `11` had no way to see this task's own `RunnerAdapter.Loops` wiring — the
result was two individually-correct, well-tested mechanisms with no code path connecting them.
See task `11`'s own Work Log above for the shared return-signature change on
`EvaluateLoopRunResumeReflexes` (`(fired, err)` → `(fired, hadCandidates, err)`) this fix
depends on.

**The fix, entirely additive to this task's own files, no changes to `enqueueLoopRunTick`
itself (per the fix's own explicit "what not to do"):**
- New `internal/loop/tick_resume.go`: `TickResumeBridge` wraps a `*LoopEngine` and a
  `*reflexes.Engine`, implements `scheduler.LoopResumer`'s exact
  `Resume(ctx, loopRunID string) (LoopResult, error)` shape structurally (no import of
  `internal/scheduler` — that would cycle back into `internal/loop`). Its `Resume` calls
  `service.EvaluateLoopRunResumeReflexes` with a headless `reflexes.State{}` first: `fired`
  means the resume already happened inside that call (re-fetches the LoopRun row for the
  return value, does not call `Resume` again); `hadCandidates && !fired` means a
  `resume_loop_run` reflex is attached but hasn't triggered yet (returns the still-waiting
  state untouched, deliberately does NOT blind-resume); `!hadCandidates` (no reflex attached
  at all — the plain durable-preset/just-retry case this task originally scoped) falls back to
  a direct `Engine.Resume` call, preserving this task's original default behavior exactly.
- `cmd/nanite/main.go`: `RunnerAdapter.Loops` now wired to
  `loop.NewTickResumeBridge(loopEngine, container.ReflexEngine)` instead of the bare
  `loopEngine`. `container.ReflexEngine` is a new exported field on `*service.Container`
  (`internal/service/container.go`) — the fix's one real correction to its own stated plan:
  the plan assumed "whatever `*reflexes.Engine` reference already exists in `main.go`," but
  verification found no such reference existed outside `service.NewContainer`'s own local
  scope (the FU-30 reflex engine built at container.go:950 was passed into `ChatServiceConfig`
  but never attached to the returned `*Container`). Exposing it as a new field — the same
  already-established pattern `ReminderEngine`/`LoopDetector` use for engines built inside
  `NewContainer` but needed by later main.go wiring — is the minimal fix consistent with the
  plan's actual intent ("reuse the one real instance, don't construct a second"), corrected
  here rather than treated as a blocker.
- New end-to-end test `internal/loop/tick_resume_test.go`, three real scenarios against a
  genuine `waiting_on_escalation` LoopRun (same `Budget{MaxIterations: 2}` →
  `waiting_on_escalation` → real Resume-driven 3rd-iteration-completes-the-goal setup task
  `11`'s own `reflex_resume_test.go` established): an attached reflex whose trigger evaluates
  false via the bridge's real headless state (`scope_tier` predicate node compared against a
  non-matching value — chosen because the bridge's fixed `Resume(ctx, loopRunID)` signature,
  matching `scheduler.LoopResumer` exactly, leaves no way to inject a live `State.Events`
  signal the way task `11`'s own direct-call test could) does not resume; the same predicate
  compared against its default empty value (`evalStringEquals`'s documented trivial-match
  behavior against an unset signal) genuinely resumes via the reflex path (confirmed via
  re-fetched LoopRun state reaching `completed` and the reflex's own `fired_count`
  incrementing); no reflex attached at all falls back to a direct blind resume, reaching the
  same real `completed` state.

**Verification, re-run after the fix (again read directly from real command output, exit
codes checked immediately, never via a piped/masked exit code):** `go build ./cmd/nanite/` —
exit 0. `go vet ./...` — exit 1, the same four pre-existing `internal/service/container.go`
lostcancel findings this task's own Work Log already documented above, confirmed via
`git blame` to trace to commits `76df826a3`/`7a0e37936` (months before this fix), unrelated to
any file this fix touched. `go test ./...` — exit 0, every package `ok` or
`[no test files]`, zero `FAIL` lines, including
`ok github.com/hollis-labs/nanite/internal/loop 29.429s` (fresh, includes the three new
`TestTickResumeBridge_*` tests) and `ok github.com/hollis-labs/nanite/internal/service
118.050s` (the new `container.ReflexEngine` field's package).

## Review notes

**First pass (2026-08-21, pre-fix)**: dispatch plumbing (`JobTypeLoopRunTick`, the no-op
guard, the deterministic dedup, wiring) all confirmed correct and well-tested. One real
finding: `tick_schedule.go`'s "deliberately not gated on a durable preset marker" doc comment
overstated that a spurious tick was always a cheap no-op — true only once a reflex predicate
actually gets evaluated, which nothing did at the time. A second, separate, non-blocking
finding was also surfaced: `decide.go`'s `WAIT`/`ESCALATE` reasoning-fallback prompt has no
differentiation criteria, and this task is the first to attach a real behavioral consequence
(auto-resume) to that distinction. Not marked `reviewed` pending the doc-comment fix.

**Resolution**: the doc-comment overstatement is fixed as part of the 2026-08-21/22
integration fix documented above in this file's own Work Log — the bridge now genuinely makes
a spurious tick a no-op by checking for an attached reflex first, so the comment's corrected
claim is now accurate. The `WAIT`/`ESCALATE` prompt-ambiguity finding is deliberately **not**
fixed here — logged in `TASKS/ESCALATIONS.md` as an explicit, named follow-up candidate for
task `13` or a fast-follow, per that entry's own reasoning (a real behavior change to an
already-reviewed LLM prompt deserves its own dedicated review, not a bundled side-fix; no live
traffic depends on this yet).

**Second pass (2026-08-22, post-fix)**: a fresh reviewer independently re-verified the
integration fix in full (`internal/loop/tick_resume.go`'s three-case control flow, the
`EvaluateLoopRunResumeReflexes` signature change and all its call sites, the three new
end-to-end tests, the `container.go`/`main.go` wiring, the import-cycle claim, and the
corrected doc comment) and confirmed it sound — no double-resume, no blind-resume-over-an-
unfired-reflex regression, single shared `ReflexEngine` instance, no import cycle. `go
build`/`go vet`/`go test` all verified with real, unmasked exit codes both before and after the
fix. This task's own "Done means" are now fully satisfied: the real creator (task `08`'s
`DecisionWait` branch via `tick_schedule.go`) exists, dispatch plumbing is tested, and the
mechanism is now genuinely reachable end-to-end via the real evaluation cadence, not just
unit-tested in isolation.
