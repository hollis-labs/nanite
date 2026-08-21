# Loops — implementation

Implements `docs/engineering/architecture/21-loops.md`, the design produced by a dedicated
architecture-alignment session (2026-08-21, operator-signed-off) that reviewed an external
proposal against `agentworkflow`, `internal/agent/reflexes`, Scheduling
(`12-scheduling.md`), and Teams (`15-teams.md`) before proposing anything. That session
changed **no code or schema** — design only, and its own "Status" section explicitly
deferred implementation, "not yet filed." This folder is that follow-up planning pass's
output.

**Not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence.** A sibling to
`TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`,
`TASKS/teams/`, `TASKS/agent-host-acp/`, `TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`,
and `TASKS/skills/` — kept in its own top-level `TASKS/` subfolder for the same reason those
are: this work wasn't part of the original plan.

## Read before starting any task here

1. `docs/engineering/architecture/21-loops.md` — the full design: the core split
   (`Workflow` = prescribed execution, unchanged; `Goal` = desired state, new; `Loop` =
   convergent repetition, new), Decision 1 (`LoopRun` is a new peer entity to `WorkflowRun`,
   *not* a `WorkflowRun` itself — the opposite of Teams' `TeamRun`-IS-a-`WorkflowRun`
   collapse, and why), Decision 2 (`Goal` is first-class with its own lifecycle from day
   one, not embedded state), the continuation-policy `decide()` sketch, the trigger surface,
   the "New mechanism vs. reuse" ledger, the illustrative schema, the Guardrail diagram, and
   the explicit "What this session did not decide" list (§ below resolves every item on it).
2. **This batch's task files carry real, load-bearing corrections and decisions found by
   this planning session's own research against the actual code — read the specific task's
   Context before assuming the design doc's prose is literally accurate.** In short, so you
   don't have to rediscover them (full detail under "Load-bearing corrections," below):
   `internal/workflow.LoopStep` (the legacy, unrelated pipeline primitive) is **dynamically
   reachable today** via `POST /api/workflows/runs` with arbitrary YAML — not just
   theoretically present, as the design doc's own "not yet checked" framing left open; the
   design doc's claim that Loop's event/predicate trigger can "fully reuse" the reflex
   trigger-spec AST the way Team's flex-step exit trigger does is **incomplete** — flex's own
   reuse is a lazy re-check piggybacked on an unrelated caller's `.Resume()`, not a real
   push, and Loop needs an actual new push mechanism (task `11`); `WorkflowLauncher` has no
   direct `.Resume` method — resume goes through `GetEngine(name)` then that engine's own
   `.Resume`, with A2A's `TaskManager` as the only real caller precedent today (task `08`).
3. `docs/engineering/GLOSSARY.md` — **Goal**, **Loop**, and **LoopRun** entries already
   landed this session (the design-alignment session itself added them) — read them, don't
   re-add them. Check before introducing any *other* new name.
4. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer
   discipline, and escalation rules every task file below follows.
5. `internal/service/team_compiler.go` and `internal/service/team_run_launcher.go` — the
   direct structural precedent for `internal/loop`'s own engine/launcher: `CompileTeam`
   compiles a saved definition into an ordinary `agentworkflow.WorkflowDefinition`;
   `TeamRunLauncher` wraps a `*WorkflowLauncher` field and drives it. `LoopEngine`/
   `LoopLauncher` follow the same shape one level up, per the design doc's own sketch.

## Load-bearing corrections and decisions made this planning session

Found or decided during this session's own research (four parallel read-only dispatches
against current code), not silently baked into a task's Context alone — logged in full in
`TASKS/ESCALATIONS.md`'s 2026-08-21 "Loops planning" entry:

1. **`internal/workflow.LoopStep` is live-reachable, not just theoretically present.**
   `internal/workflow/loader.go:194-208` has a live `case "loop":` branch, and
   `POST /api/workflows/runs` (`internal/api/workflows.go:78-135`, `handleRunWorkflow`)
   calls `workflow.Load(body)` directly on an arbitrary request body — any caller can POST
   YAML containing `type: loop` right now and it will parse and execute. No committed YAML
   uses it, and `Gate` can't be set via YAML (`loader.go`'s own comment: "not configurable
   via YAML — set programmatically"), so a dynamically-POSTed loop step always runs to
   `MaxIter` (default 5) with no early-exit condition. No evidence of real traffic hitting
   it. This does not change `21-loops.md`'s own call ("this design does not propose
   retiring it... a separate 'kill dead code' pass, not a decision made here") — it sharpens
   the record from "not yet checked" to "checked: reachable, unused in practice." No task in
   this batch touches `internal/workflow` or that endpoint; flagged here so a future auditor
   doesn't have to re-derive it.
2. **The flex-step trigger "reuse" is a pull, not a push — Loop's event/predicate trigger
   needs a real new mechanism.** `internal/service/workflow_engine.go:150-169`'s own comment
   is explicit that a flex step's exit trigger gets "a real, active re-check... on every
   Resume-driven pass through this loop" — i.e. nothing fires the check; it only runs when
   *something else* already calls `.Resume(ctx, runID, ...)` for an unrelated reason. The
   only real external driver of that today is A2A's `TaskManager` task-poll path
   (`a2a_task_manager.go:744`). A `WAIT`-status `LoopRun` with no A2A task attached has
   nothing to piggyback on. Task `11` resolves this with a real, new, legible reflex action
   kind (`resume_loop_run`) that calls `LoopEngine.Resume` directly when its trigger fires —
   not a generic `callback` kind, which `10-reflex-action-taxonomy.md:120` already rejected
   for exactly this illegibility reason, and not a silent assumption that "reuse" alone
   solves it.
3. **`StepKindLoop`'s kind-CHECK and `RunStatusWaitingOnLoop`'s status-CHECK are two
   separate migrations, landing in two separate phases** — mirrors Teams' own precedent
   exactly (`130_workflow_run_steps_flex_kind.sql`, Phase 1 schema, widened `kind` only;
   `133_workflow_run_flex_waiting_status.sql`, landed alongside the real executor work,
   widened both tables' `status`). Task `06` (Phase 1) does the former; task `09` (Phase 2)
   does the latter, together with the actual executor wiring — not one combined migration.
4. **`workflow_runs.loop_run_id`/`loop_iteration` need no table rebuild** — plain nullable
   `ADD COLUMN` + index, identical to migration `131_agent_reflexes_workflow_run_scoping.sql`
   (task `05` mirrors it exactly), since neither new column carries a `CHECK` constraint.

## What this session decided on every item `21-loops.md`'s own "What this session did not
decide" list left open

Per this batch's own scoping calls, matching the "pick a working default, flag it
provisional" discipline `TASKS/teams/`, `TASKS/scheduling/`, and `TASKS/skills/` each
applied to their own design doc's open questions — do not expand any task below beyond
these without a fresh operator conversation:

- **Exact migration DDL** — each schema task below specifies concrete, illustrative DDL;
  workers document their own call if they deviate, same discipline as every prior batch.
- **`goal_evidence` free-text evidence** — decided **no**: every row points at something
  structured (`ref_table`/`ref_id` required, never null together). A human's written
  acceptance note is captured as a real `event_log` row (`ref_table='event_log'`) and
  pointed at from there, not a new free-text column on `goal_evidence` itself — keeps the
  "thin pointer table, not a duplicate content store" discipline the design doc states as
  load-bearing (task `02`).
- **REARCHITECT's concrete mechanism** — decided: REPLAN plus a revision to the Goal's
  `desired_state`/`acceptance_criteria`, computed by the *same* bare `ExecuteLLMStep`
  reasoning-fallback call the no-progress-streak case already needs (task `07`) — not a
  separate, literal architect-agent dispatch. Reuses one mechanism instead of inventing a
  second "isolated model call" shape.
- **Concurrent `LoopRun`s against one `goal_id`** — decided **no** for v1: `LoopLauncher.Launch`
  (task `10`) rejects a launch against a `goal_id` that already has a `running`/`waiting_*`
  `LoopRun`. Simplest safe default; revisit only if a real competing-strategies use case
  shows up.
- **`budget`/`on_exhausted` vocabulary** — decided: `on_exhausted ∈ {"escalate","fail"}`,
  default `"escalate"`. Narrower than Scheduling's `retry`/`disable`/`notify` — `retry`
  doesn't apply once the *total* budget (not one iteration) is exhausted, and
  `disable`/`notify` are Schedule-specific concepts with no `loop_runs` analog (task `07`).
- **`loop_run_tick`'s payload shape** — decided, now that Scheduling has landed:
  `LoopRunTickPayload{LoopRunID string}`, matching the existing 4 job types' exact
  one-required-field-plus-`omitempty`-context convention in
  `internal/scheduler/runner_adapter.go` (task `12`).
- **Auth/permission model for launch/cancel/force-resolve** — decided: standard operator
  auth, no new provenance-tier gating — matches Scheduling's own non-locked call for
  `09-operator-http-api.md` (task `10`).
- **Presets as DB rows or Go constants** — decided: **Go-coded constants** for v1 (a
  `map[string]LoopPreset` registry, no new table) — pure configuration, no per-preset engine,
  and this repo's own "start narrow" bias (the one Decision 2 explicitly diverged from only
  for Goal, not extended here). `ralph` is the one preset this batch builds and tests fully
  end-to-end, per the design doc's own "falls out of this for free" framing; the rest of the
  named list (`test-fix`, `review-fix`, `plan-execute`, `queue-drain`, `durable`,
  `self-improve`) are registered as named stubs with a documented shape but not fully
  authored — a real follow-up, not silently dropped (task `13`).
- **Implementation sequencing/task breakdown** — this batch, below.

## Task sequence

**Phase 1 — Schema & storage foundation.** New tables/columns and Go types only — no
runtime behavior. All six tasks touch `internal/store/migrations/`; real cross-batch
numbering-collision risk, same pattern every concurrent batch has hit — `TASKS/plugin-system`
provisionally claims `135`, `TASKS/skills` claims `136`-`137`, and `TASKS/agent-host-acp`/
`TASKS/filesystem-snapshots` may also be in flight. Latest migration on disk at this
planning session's authoring time (2026-08-21) is `134_agent_profiles_protocol_transport.sql`;
this batch provisionally claims `138` onward in the order below — **whichever task is
dispatched, or whichever other in-flight batch lands first, must re-list the migrations
directory and renumber.**

| Task | Depends on |
|---|---|
| `01-goals-schema.md` | none |
| `02-goal-evidence-schema.md` | `01` (`goal_id` FK) |
| `03-loop-runs-schema.md` | `01` (`goal_id NOT NULL` FK) |
| `04-loop-run-iterations-schema.md` | `03` (`loop_run_id` FK) |
| `05-workflow-runs-loop-scoping-columns.md` | `03` (`loop_run_id` FK target must exist) |
| `06-stepkindloop-schema.md` | none (parallel-safe — different table, `workflow_run_steps.kind`) |

Parallel groups: Wave 1 — `01`, `06` (fully independent). Wave 2 — `02`, `03` (both need
`01`, not each other). Wave 3 — `04`, `05` (both need `03`, not each other).

**Phase 2 — Runtime engine.** The continuation policy, the engine driving iterations, and
the contained-loop step executor — strict-ish order, each is a real prerequisite for the
next.

| Task | Depends on |
|---|---|
| `07-loop-continuation-policy.md` | `01`, `02`, `03`, `04` |
| `08-loop-engine-core.md` | `03`, `04`, `07` |
| `09-stepkindloop-executor-and-waiting-status.md` | `06`, `08` |

**Phase 3 — Trigger surface.** Three mutually parallel-safe tasks (different files/
subsystems each) once `08` lands — same shape Scheduling's own Phase 2 producers took.

| Task | Depends on |
|---|---|
| `10-loop-launcher-and-api.md` | `08` |
| `11-loop-event-predicate-trigger.md` | `08` |
| `12-loop-run-tick-scheduled-trigger.md` | `08` |

**Phase 4 — Presets.**

| Task | Depends on |
|---|---|
| `13-loop-presets.md` | `07`, `08`, `10` |

See `TASKS/INDEX.md`'s own new section for status tracking as these land.

## What this batch does NOT do

- **`internal/workflow.LoopStep`** — not touched, not retired, not gated. See "Load-bearing
  corrections" item 1.
- **Mid-run Goal decomposition logic** (`parent_goal_id` subgoal recomputation, `21-loops.md`
  §16) — the column exists (task `01`) but no engine walks it. A real follow-up, not this
  batch's job.
- **Deeper no-progress/convergence heuristics** (same-tests-failing, semantic-state-unchanged
  detection) — v1 is a streak counter against a budget threshold, exactly as the design doc's
  own ledger scopes it (task `07`).
- **Any frontend/admin-UI surface** for Goals or Loops — a separate stream, matching every
  prior batch's own scope fence.
- **Wiring `wrapper.Config.Policy`/`policy.Engine`** for loop-launched agent turns — confirmed
  dormant in Nanite today (`TASKS/skills/README.md`'s own finding); not this batch's concern,
  loop-launched `WorkflowRun`s get whatever enforcement the workflow engine already applies.
