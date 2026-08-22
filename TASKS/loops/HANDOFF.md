# Loops — Handoff to next work

**For:** whoever picks up further Loop/Goal work next — most plausibly someone building a real
`test-fix`/`review-fix`/`durable`/etc. preset, hardening `decide.go`'s reasoning prompt, or
wiring a frontend surface for Goals/Loops. Assume zero shared context with this batch.

**Batch status:** all 13 tasks (`01`-`13`, all 4 phases) are `reviewed` per `TASKS/INDEX.md`.
Every task where a first-pass review found a real bug was fixed and independently
re-reviewed before being marked done. This doc and `SUMMARY.md` are the closing artifacts.

---

## What actually shipped

**Phase 1 — schema & storage foundation.** Six migrations (landed in a different order/numbers
than originally planned — see "What changed from plan" below), all pure schema + Go CRUD, no
runtime behavior:

- `135_goals.sql` (task `01`) — `goals` table, full lifecycle (`draft → defined → active →
  blocked/satisfied/failed/cancelled/superseded`), `internal/store/goals.go`.
- `137_goal_evidence.sql` (task `02`) — `goal_evidence` thin pointer table (`ref_table`/`ref_id`
  always required, never free-text), `internal/store/goal_evidence.go`, including the real
  `EvaluateGoalEvidence`/`EvidenceSatisfiesGoal` four-clause `goal_met` walk.
- `138_loop_runs.sql` (task `03`) — `loop_runs`, a **new peer entity to `WorkflowRun`, not a
  `WorkflowRun` itself** (own `id`, own lifecycle), `internal/store/loop_runs.go`, including
  `Budget`/`OnExhausted ∈ {escalate, fail}`.
- `139_loop_run_iterations.sql` (task `04`) — append-only per-iteration history,
  `internal/store/loop_run_iterations.go`.
- `140_workflow_runs_loop_scoping.sql` (task `05`) — `workflow_runs.loop_run_id`/
  `loop_iteration` (nullable, no CHECK, plain `ADD COLUMN`), plus
  `Store.UpdateWorkflowRunLoopScope` (a narrow updater, since `WorkflowLauncher.Launch` has no
  natural place to plumb these two fields through — see below).
- `136_workflow_run_steps_loop_kind.sql` (task `06`) — widens `workflow_run_steps.kind`'s CHECK
  to add `'loop'`, plus the `agentworkflow.StepKindLoop` constant. Schema-only; task `09` is the
  real executor.

**Phase 2 — runtime engine.**

- `internal/loop/decide.go` (task `07`) — `Decide(ctx, exec, goal, goalMet, evaluation,
  history, budget, policy) (Decision, error)`, the deterministic-first continuation policy:
  `goal_met → COMPLETE`; budget exhausted → `ESCALATE`/`FAIL` per `OnExhausted`; no-progress
  streak ≥ threshold → a bare `StepExecutor.ExecuteLLMStep` reasoning-fallback call (same shape
  `VerifyModeAgent` already uses) resolving to one of `RETRY|REPLAN|REARCHITECT|WAIT|ESCALATE|
  FAIL`; otherwise `CONTINUE`. A malformed/unparseable reasoning response always returns an
  `error`, never silently defaults to `CONTINUE`. `internal/loop/doc.go` disambiguates this
  package from `internal/loopdetect` and legacy `internal/workflow.LoopStep`.
- `internal/loop/engine.go` (task `08`) — `LoopEngine.Run`/`.Resume`, wrapping
  `*service.WorkflowLauncher` the same way `TeamRunLauncher` wraps it. Drives iterations
  synchronously within one call for a bounded loop; a `WAIT`/`ESCALATE` decision persists
  `loop_runs.status` and returns without blocking; `Resume` re-enters. Enforces one active
  `LoopRun` per `goal_id`. `loop_runs.continuation_policy_json` was defined here as
  `loopRunPersistentConfig` (continuation policy + `AgentProfileID`/`ProjectID`/
  `ParentSessionID`/`TimeoutSeconds`/`WorkflowParams`) — the persisted shape a bare
  `Resume(ctx, loopRunID)` needs to relaunch further iterations with zero caller-supplied
  inputs.
- `internal/service/workflow_engine_loop.go` + migration `141` (task `09`) — the
  `StepKindLoop` step executor: a Workflow can *contain* a Loop. `RunStatusWaitingOnLoop`
  (new, distinct from `waiting_on_gate`/`waiting_on_flex`), a real three-way precedence
  function (`waitingRunStatus`: gate > flex > loop), and a **real push mechanism** — when
  `LoopEngine` drives a `LoopRun` to a terminal state, it calls back into
  `internal/service` (via an `OuterResumeNotifier` interface) to `Resume` the *outer*
  `WorkflowRun` that's waiting on it. `deriveFromWorkflowRun` maps `waiting_on_loop` →
  `a2a.TaskStateWorking` (not `InputRequired` — a loop-waiting run isn't blocked on a human).

**Phase 3 — three trigger surfaces, all reviewed and (after a fix, see below) actually
connected to each other.**

- `internal/loop/launcher.go` + `internal/api/loops.go` (task `10`) — `LoopLauncher.Launch`/
  `.Cancel`/`.ResolveEscalation`. REST surface: `POST/GET/PATCH/DELETE /api/goals`,
  `GET /api/goals/{id}/evidence`, `POST /api/loops`, `GET /api/loops`, `GET /api/loops/{id}`,
  `POST /api/loops/{id}/cancel`, `POST /api/loops/{id}/resolve`,
  `GET /api/loops/{id}/iterations`. Standard operator auth, no new provenance tier.
- `resume_loop_run` reflex action kind, migration `142` (task `11`) — a real, named reflex
  action kind (not a generic `callback`, which the reflex-taxonomy doc already rejected for
  illegibility reasons). Reuses the existing predicate/event/interval trigger-spec AST
  unchanged. `internal/service/loop_resume_reflex.go`'s `EvaluateLoopRunResumeReflexes` is the
  real handler.
- `loop_run_tick` scheduled `JobType`, migration `143` (task `12`) — the fifth `JobType`
  alongside Scheduling's four. `internal/loop/tick_schedule.go`'s `scheduleLoopRunTick` is the
  real creator (wired from `LoopEngine`'s own `DecisionWait` branch — nothing else in the
  codebase creates one).

**Phase 4 — presets.**

- `internal/loop/presets.go` (task `13`) — a Go-coded `map[string]LoopPreset` registry (no DB
  table), matching `pkg/models`' own compiled-catalog convention, not `internal/store/seed.go`'s
  DB-row-seeding shape. `ralph` is built and tested fully end-to-end: `MaxIterations: 20`,
  `OnExhausted: escalate`, `MaxNoProgressIterations: 0` (a structural guarantee — combined with
  its one-`llm`-step-no-`Verify` template, Ralph's continuation policy can never reach
  `REPLAN`/`REARCHITECT`, both mechanisms deliberately redundant, both documented). The other
  six named presets (`test-fix`, `review-fix`, `plan-execute`, `queue-drain`, `durable`,
  `self-improve`) are registered, `GetPreset`-resolvable stubs with zero-`Steps`
  `DefinitionTemplate`s — `GetPreset` always succeeds; actually launching one returns
  `ErrPresetNotImplemented`. `LoopLaunchRequest.DefinitionName` checks preset names first
  (confirmed no real `WorkflowDefinition` collides with any of the seven names), then falls back
  to the ordinary workflow-definition registry.

`docs/engineering/GLOSSARY.md` carries **Goal**, **Loop**, **LoopRun**, and **LoopPreset**
entries (Goal/Loop/LoopRun were added by the design-alignment session itself; LoopPreset was
added by task `13`).

---

## What changed from the original plan, and why

- **Migration numbers landed completely differently from the plan's provisional `138`-`144`
  sequence**, because two other concurrent batches (`plugin-system`, `skills`) were also
  claiming numbers off the same `134` base in isolated worktrees at the same time. Real landed
  order: `135`(01) → `136`(06) → `137`(02) → `138`(03) → `139`(04) → `140`(05) → `141`(09) →
  `142`(11) → `143`(12). If you're auditing this batch's migrations, use this list, not the
  task files' own "provisionally claims N" text.
- **A same-batch migration collision happened anyway, inside Wave 1**: tasks `01` and `06` were
  dispatched fully in parallel, each in its own isolated worktree, and each independently
  found `134` as the highest number visible from its own worktree — so both wrote `135`.
  Resolved at merge time by renumbering `06` to `136` (`TASKS/ESCALATIONS.md`, 2026-08-21,
  "Loops Wave 1... same-batch migration-number collision"). No operator involvement needed.
  Lesson logged for future batches: expect same-wave collisions on migration numbers as the
  default outcome of parallel-worktree dispatch, not the exception.
- **The design doc's own stated import-cycle risk direction was backwards.** `21-loops.md` and
  tasks `09`/`11`'s own Context sections guessed `internal/service` already imports
  `internal/loop`. The real code is the opposite: `internal/loop` imports `internal/service`
  (for `WorkflowLauncher`). This flips which direction actually needs an interface seam:
  - The **notify** direction (`internal/loop` calling back into `internal/service` to resume an
    outer `WorkflowRun`, or to fire a reflex) has zero cycle risk on its own, but was still
    built as a small interface (`OuterResumeNotifier`, `LoopRunResumer`) for the same
    narrow-dependency-footprint reason `workflow_engine_flex.go`'s own collaborator interfaces
    exist.
  - The **launch** direction (`internal/service`'s `StepKindLoop` executor needing to call
    `LoopEngine.Run`) is the genuine cycle risk, since `internal/service → internal/loop` would
    close a cycle back through `internal/loop`'s existing import of `internal/service`. Resolved
    with `LoopStepLauncher`, an interface declared in `internal/service` and satisfied
    structurally by a method added directly onto `*loop.LoopEngine`.
  If you touch either direction again, re-verify with `grep` before assuming either doc's prose
  — both got the direction backwards at authoring time.
- **Task `07`'s `evidenceSatisfiesGoal` duplicated task `02`'s `goal_met` formula**, then task
  `08` removed the duplicate: `Decide`'s signature changed from taking `[]GoalEvidence` to
  taking a precomputed `goalMet bool`, and `internal/loop`'s local copy of the four-clause
  formula was deleted entirely. `internal/store/goal_evidence.go`'s `EvaluateGoalEvidence` is
  now the one and only implementation. (`TASKS/ESCALATIONS.md`, 2026-08-21, "Task `07`'s
  `Decide()` re-implements task `02`'s goal-evidence formula locally.")
- **A real correctness bug in `driveIterations`'s bounded-loop guard** conflated the caller's
  own `ctx` dying (client disconnect, shutdown) with the derived `MaxRuntimeSeconds` deadline
  genuinely firing — both were diagnosed as "budget exhausted." Found by task `08`'s review,
  fixed (check `ctx.Err()` before `runCtx.Err()`), and re-verified with a regression test that
  fails against the pre-fix code and passes against the fix.
- **The single biggest deviation: Phase 3's two "mutually parallel-safe" trigger tasks (`11`,
  `12`) shipped individually correct but mutually disconnected mechanisms.** See the next
  section — this is a real architecture lesson, not just a bug.

---

## Gotchas discovered mid-batch that the next session should know

**The `11`/`12` integration gap — read this even if you touch neither task again.** Tasks `11`
(event/predicate reflex trigger) and `12` (scheduled tick trigger) were dispatched genuinely in
parallel, each in its own isolated worktree, per the batch's own Phase 3 plan ("three mutually
parallel-safe tasks"). Each was independently correct and independently fully tested:

- Task `11` built `resume_loop_run` and, in its own "What to do" investigation, correctly
  concluded that the *only* real evaluation cadence for this reflex kind in production would be
  task `12`'s scheduled tick (a `resume_loop_run` reflex has no live chat session to piggyback a
  per-turn evaluation on).
- Task `12` built `enqueueLoopRunTick`, which — because its own task file predates task `11`'s
  final reflex design and was never told to check for one — called `LoopEngine.Resume` directly
  and unconditionally, never consulting `EvaluateLoopRunResumeReflexes` at all.

The result: `resume_loop_run`'s entire reflex path was real, correct, and unit-tested in
isolation, and **completely unreachable in the running system** — a scheduled tick would blind-
resume a `LoopRun` regardless of whether an attached reflex's own trigger had actually fired.
Neither task's own worker could have caught this (concurrent, isolated worktrees, no visibility
into the other's final code); the Orchestrator caught it while reviewing task `11`'s diff before
merging (`TASKS/ESCALATIONS.md`, 2026-08-21, "Phase 3's two parallel trigger tasks... built
compatible but disconnected mechanisms").

**Fixed** with `internal/loop/tick_resume.go`'s `TickResumeBridge` — wraps both `*LoopEngine`
and `*reflexes.Engine`, checks for an attached-but-unfired `resume_loop_run` reflex first (and
does **not** resume if one exists and hasn't fired), falls back to a direct blind resume only
when no reflex is attached at all (preserving `12`'s original plain-durable-poll default).
`EvaluateLoopRunResumeReflexes`'s return signature was extended from `(fired, err)` to `(fired,
hadCandidates, err)` so a caller can actually distinguish "no reflex attached" from "one is
attached, just hasn't fired" — the exact ambiguity that let the original gap go undetected.
Independently re-reviewed sound, 2026-08-22 (`TASKS/ESCALATIONS.md`, same entry, "Integration
fix landed and independently re-reviewed").

**The general lesson, worth carrying into any future batch using this same parallelization
pattern**: "mutually parallel-safe" (different files/subsystems) does not mean "the resulting
mechanisms will actually be wired to each other." When two parallel tasks each build one half of
a producer/consumer relationship, verify the *actual* call graph after both merge — a diff
review of each task in isolation will not surface this; only tracing the real, merged call path
does. Consider, for a future batch with this shape, explicitly assigning one task to build both
halves, or a fourth, dedicated integration task, rather than trusting two isolated correct halves
to compose.

**Process incidents, both real but ultimately harmless — fixed at the tooling/agent-definition
level, not just logged:**

- **`git stash` misuse, twice more in this batch** (on top of two prior occurrences in other
  batches). Task `01`'s own worker ran `git stash`/`git stash pop` mid-task (its own new files
  were untracked, so nothing was pushed — the `pop` instead popped an old, unrelated,
  pre-existing stash entry, producing merge conflicts the worker recovered from cleanly, no data
  lost — `TASKS/ESCALATIONS.md`, 2026-08-21, "fourth occurrence"). Then, during the `11`/`12`
  integration fix's own review, the **reviewer** did the identical thing (`TASKS/ESCALATIONS.md`,
  2026-08-22, "fifth occurrence... this time from a reviewer") — self-recovered again, no data
  lost. This fifth occurrence is why `.claude/agents/reviewer.md` now carries the same explicit
  "never run `git stash`" warning `.claude/agents/worker.md` already had — the rule had only ever
  been fixed on the worker side, and a reviewer has the identical `Bash` tool access. **If you
  dispatch any further worker/reviewer against this codebase, both agent-type definitions now
  carry the warning directly — verify it's still there if this recurs.**
- **A real pipe-masking bug in the Orchestrator's own verification, not a worker's.** After
  merging Wave 2 (tasks `02`, `03`), the Orchestrator ran `go test ./... 2>&1 | tail -50` and
  read the background task's own "exit 0" as confirmation the full suite passed — but that exit
  code was `tail`'s, not `go test`'s. `go test ./internal/store/...` was actually failing to
  compile (both workers had independently declared an identical `makeTestGoal` test helper in
  their own isolated worktrees; each passed individually, the merged tree didn't). The fresh
  reviewer caught it by running `go vet` directly, unpiped. Fixed with a mechanical dedup.
  **Going forward, every verification in this batch (and it's worth propagating this beyond
  Loops) redirects `go build`/`go vet`/`go test` output to a file and checks `$?` immediately,
  never through a pipe to `tail`/`grep`/anything else** — every later task's Work Log in this
  batch explicitly documents doing this.

**Investigated and resolved, not left open.** Task `06`'s own Work Log ("Baseline checks")
reads *"confirmed pre-existing and unrelated to this task by stashing this task's changes and
re-running `go vet ./...`, which reproduces the identical four findings on the unmodified base
branch."* That phrasing describes the same forbidden `git stash` action the batch's own
escalations later flag as incident four (task `01`) and five (the `11`/`12` reviewer) — never
logged as an incident at the time. The Orchestrator confirmed directly (`TASKS/ESCALATIONS.md`,
2026-08-22, "A confirmed, previously unflagged occurrence found retroactively"): task `06` was
dispatched in Wave 1, in parallel with task `01`, **before** the explicit "never run `git
stash`" instruction was added to any worker prompt in this batch (that instruction only started
appearing from Wave 2 onward, after task `01`'s own incident surfaced it). This is realistically
the **first or second** chronological occurrence of the pattern in this batch, not a separate
sixth one. No corruption evidence exists — task `06` was reviewed clean, merged, and built upon
by every subsequent task without incident, and every post-merge `go build`/`go vet`/`go test`
check the Orchestrator ran throughout the rest of the batch (dozens of checkpoints) came back
clean. Not escalated further: the underlying gap (workers dispatched before the warning existed
had no way to know) is already structurally closed — the warning now lives in
`.claude/agents/worker.md`/`reviewer.md` directly, not just per-dispatch prompt text a future
kickoff might omit.

**One other minor process note:** task `10`'s fresh reviewer had its own Bash tool fail entirely
(a sandbox misconfiguration, not this batch's fault) and could not independently run
`go build`/`go vet`/`go test`/`git status` against the merged commit. The Orchestrator ran all
four directly instead and folded the results into that task's own Review notes. The content/logic
review itself (a thorough static read-through) was still independent — only the mechanical
build/test verification wasn't run by the reviewer's own tool invocation. Not a reason to
distrust task `10`, but worth knowing if you're deciding how much weight to put on "independently
verified" for that one task specifically.

---

## Concrete verification steps

Don't just trust `TASKS/INDEX.md`'s status column — confirm these directly.

**Migrations exist in the real landed order:**
```
ls internal/store/migrations/ | sort -t_ -k1 -n | tail -9
```
Expect, in order: `135_goals.sql`, `136_workflow_run_steps_loop_kind.sql`,
`137_goal_evidence.sql`, `138_loop_runs.sql`, `139_loop_run_iterations.sql`,
`140_workflow_runs_loop_scoping.sql`, `141_workflow_run_waiting_on_loop_status.sql`,
`142_agent_reflex_resume_loop_run.sql`, `143_agent_schedules_loop_run_tick_job_type.sql`.

**Tables/columns:**
- `goals`, `goal_evidence`, `loop_runs`, `loop_run_iterations` exist (`sqlite3 <db> ".tables"`).
- `workflow_runs.loop_run_id`/`loop_iteration` nullable columns exist.
- `workflow_run_steps.kind`'s CHECK includes `'loop'`; `workflow_run_steps.loop_run_id` column
  exists; `workflow_runs.status`/`workflow_run_steps.status` CHECKs both include
  `'waiting_on_loop'` (`sqlite3 <db> ".schema workflow_run_steps"` / `".schema workflow_runs"`).
- `agent_reflexes.action_kind`'s CHECK/FK accepts `'resume_loop_run'`; `reflex_action_kinds` has
  a `resume_loop_run` row; `reflex_action_kind_provenance_allow` allows it for `system`/
  `operator` only (not `plugin`).
- `agent_schedules.job_type`'s CHECK includes `'loop_run_tick'`.

**Go surfaces (grep to confirm they exist, then read):**
- `internal/loop/decide.go` — `func Decide(...)`.
- `internal/loop/engine.go` — `LoopEngine.Run`/`.Resume`, `evaluateDecideAndAct`,
  `driveIterations`.
- `internal/loop/launcher.go` — `LoopLauncher.Launch`/`.Cancel`/`.ResolveEscalation`.
- `internal/loop/presets.go` — `GetPreset`, `PresetNames`, the `ralph` entry.
- `internal/loop/tick_resume.go` — `TickResumeBridge`, the `11`/`12` bridge.
- `internal/loop/tick_schedule.go` — `scheduleLoopRunTick`, the real `loop_run_tick` creator.
- `internal/service/workflow_engine_loop.go` — `startLoopStep`, `recheckLoopStep`,
  `waitingRunStatus` (gate > flex > loop precedence).
- `internal/service/loop_resume_reflex.go` — `EvaluateLoopRunResumeReflexes`.
- `internal/scheduler/runner_adapter.go` — `JobTypeLoopRunTick`, `enqueueLoopRunTick`.
- `internal/api/loops.go` — the full `/api/goals`/`/api/loops` route set.

**Build/test:**
```
go build ./cmd/nanite/
go vet ./...
go test ./...
```
Expect all clean except two pre-existing, unrelated `internal/service/container.go`
`stopReaper`/`stopRuntimeReaper` lostcancel findings (confirmed by every single task in this
batch, via `git log`/`git blame`, to predate this batch by months — commits `76df826a3`/
`7a0e37936`). If `go vet ./...` reports anything else, that's new and worth investigating.

Targeted, if you want faster signal: `go test ./internal/loop/... ./internal/store/...
./internal/service/... ./internal/scheduler/... ./internal/api/...`.

**A real end-to-end Ralph launch via `/api/loops`** (the exact recipe task `10`'s own Work Log
used for its live dogfeed — no LLM credentials required, uses a `kind: tool` step calling the
always-available `tool_list` self-tool):
1. `POST /api/agents` — create a real agent profile.
2. `POST /api/loops` — `{"definition_name": "ralph", "inline_goal": {...}, "budget_overrides":
   {"max_iterations": 1}}` (a `MaxIterations: 1` override against Ralph's default `20` will
   reach `waiting_on_escalation` quickly for a smoke test, since no goal evidence will have been
   recorded).
3. `GET /api/loops/{id}` — confirm `status: "waiting_on_escalation"`.
4. `GET /api/loops/{id}/iterations` — confirm one completed `loop_run_iterations` row with a
   real `workflow_run_id`.
5. `POST /api/loops/{id}/resolve` with `{"force_complete": true}` — confirm the status
   transitions to `completed`.

**Integration test proving the Workflow-contains-Loop push actually fires** (not a
manually-triggered `.Resume()` standing in for it): `go test ./internal/loop/... -run
TestStepKindLoop_OuterWorkflowRun_ResolvesViaRealPush_NotManualResume -v`.

**Integration test proving the `11`/`12` bridge actually connects them:** `go test
./internal/loop/... -run TestTickResumeBridge -v`.

---

## Explicitly named follow-ups not done in this batch

None of these are silently dropped — every one is named directly in the relevant task file's
own Work Log/Review notes, and none blocks anything this batch itself claims to ship:

1. **The `WAIT`/`ESCALATE` prompt-ambiguity gap in `decide.go`'s `reasoningSystemPrompt`.**
   The reasoning-fallback LLM call is offered `WAIT`/`ESCALATE` as two flat, undefined options
   with no differentiation criteria telling the model that `WAIT` means "safe to auto-retry on a
   timer" and `ESCALATE` means "needs a human, do not auto-resume." Task `12` is the first task
   to attach a real behavioral consequence to that distinction (an automatic re-launch vs.
   sitting until a human acts) — nothing today makes the model's choice reliable enough to bear
   that weight. Not fixed in this batch (`TASKS/ESCALATIONS.md`, 2026-08-21, "Phase 3's two
   parallel trigger tasks," the follow-up paragraph) — deliberately, since it's a real prompt
   behavior change to an already-reviewed file, deserving its own dedicated review.
2. **Six stub presets with no real `DefinitionTemplate`**: `test-fix`, `review-fix`,
   `plan-execute`, `queue-drain`, `durable`, `self-improve`. Each is registered and
   `GetPreset`-resolvable but returns `ErrPresetNotImplemented` on launch. Notably, `durable` is
   one of the two named triggers for `loop_run_tick` in the design doc's own text — it does not
   exist as a real, distinguishable continuation-policy marker anywhere in the codebase, so task
   `12` treats *every* `WAIT` decision as the polling case, not just a hypothetical
   "durable-preset" subset (documented in `internal/loop/tick_schedule.go`'s own doc comment as a
   real follow-up for whoever builds `durable` for real).
3. **No per-launch tool+verify opt-in for `ralph`.** Ralph's `DefinitionTemplate` is one static
   value; there's no mechanism to vary the DAG *shape* per launch (only its `{{input.*}}`
   content). A caller wanting Ralph-with-verify today has to author and register their own
   `WorkflowDefinition` directly and launch against that name instead of `"ralph"`.
4. **No registry re-registration across a process restart for a `Resume` against a
   preset-launched `LoopRun`.** A preset's `DefinitionTemplate` is registered into the shared,
   in-memory `*agentworkflow.Registry` lazily on first launch. If the process restarts while a
   Ralph run is paused (`waiting_on_escalation`/`waiting_on_gate`), the registry starts empty and
   a subsequent `Resume` (which bypasses `LoopLauncher.Launch` entirely) will fail to find
   `"ralph"` until some other `Launch` call re-populates it. A defensive re-registration inside
   `Resume`/`driveIterations` when `GetPreset` succeeds but the registry lookup misses would be a
   safe, low-risk fix — not built here (task `13`'s own scope was `presets.go` plus a small
   `launcher.go` extension, not `engine.go`).
5. **Mid-run Goal decomposition** (`parent_goal_id` subgoal recomputation, `21-loops.md` §16).
   The column exists (task `01`) but no engine walks it — never in this batch's scope, per the
   batch's own README.
6. **`loop_runs.status` collapses `Decide`'s `WAIT` and `ESCALATE` outputs into the same DB
   literal** (`waiting_on_escalation`) — task `08`'s own documented v1 simplification. No
   information is actually lost (`loop_run_iterations.decision` keeps the real distinction
   per-iteration), but the coarser `loop_runs.status` column is what task `10`'s REST surface and
   task `11`'s reflex guard both read. A future migration adding a distinct `waiting` status
   (mirroring `waiting_on_flex`'s own precedent) is named as a real follow-up in task `08`'s Work
   Log, not built here.
7. **A `loop_run_tick` schedule is one-shot, not self-rescheduling.** If a tick fires and the
   external condition still isn't true, nothing re-schedules a second tick automatically — task
   `12`'s own documented limitation, left for whoever builds a real recurring polling-cadence
   contract (likely `durable`'s job, per item 2 above).
8. **A context deadline expiring *while* a `Launch` or `Decide` reasoning-fallback call is
   already in flight** surfaces as a plain propagated error, not a gracefully persisted
   `ESCALATE`/`FAIL` status — task `08`'s own documented, accepted known limitation (the tested,
   common path is the between-iterations check, not a mid-call race).
9. **Any frontend/admin-UI surface for Goals or Loops** — explicitly out of scope for this whole
   batch, per the README's own scope fence. Nothing here changes that.
10. **`Cancel` has no precondition on a `LoopRun`'s current status** (will silently overwrite an
    already-`completed`/`failed` row to `cancelled`) — flagged as a non-blocking, slightly odd
    operator-facing behavior in task `10`'s review, not fixed.

None of these are blockers for anything this batch itself claims — they're the honest list of
what a "durable"/production-hardened Loop still needs.
