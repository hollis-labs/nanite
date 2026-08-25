# Code Mode — implementation

Implements `docs/engineering/architecture/27-code-mode.md`, **approved for implementation
2026-08-21** after a follow-up research pass (folded into that doc's own text) found the real
blocker isn't the "should Code Mode be reachable outside an LLM turn" scoping question the doc
originally posed — it's that `tool_call()` inside `python_run`'s sandbox doesn't work for *any*
caller in production today, LLM-invoked or otherwise. A sibling to `TASKS/teams/`,
`TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`,
`TASKS/agent-host-acp/`, `TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`, `TASKS/skills/`,
and `TASKS/loops/` — kept in its own top-level `TASKS/` subfolder for the same reason those are:
this work originates from a dedicated architecture-review pass, not `docs/engineering/TASKS.md`'s
original plan.

## Read before starting any task here

1. `docs/engineering/architecture/27-code-mode.md` in full — short, and already reflects two
   rounds of research findings folded directly into its own text (not left as separate prose to
   reconcile). "The actual gap" section is the load-bearing one: `PythonPermChecker`/
   `PythonDispatcher` are struct fields on `SelfToolsTransport` that are never assigned outside
   test code, so every `tool_call()` from inside `python_run` either silently bypasses permission
   enforcement (nil `perm`) or fails outright (nil `dispatcher`) — unconditional on caller type,
   predating the "should this be non-agentically reachable" question entirely.
2. `docs/engineering/architecture/10-reflex-action-taxonomy.md` and `TASKS/reflex-taxonomy/` —
   **this batch's task `03` builds directly on top of an already-shipped, already-`reviewed`
   batch**, not raw design prose. `reflex_action_kinds`/`reflex_provenance_tiers`/
   `reflex_action_kind_provenance_allow` (migrations `124`, `125`) and the shared
   `reflexes.Resolve()` decision engine (`internal/agent/reflexes/resolve.go`) are real, live code
   today — see `TASKS/reflex-taxonomy/PHASE-SUMMARY.md` for confirmation all core tasks are
   `reviewed`. A new action kind is a real, if small, schema change against that live taxonomy —
   see "Load-bearing correction" #1 below; do not assume "no schema needed" from the outer
   planning instruction that kicked off this batch.
3. `TASKS/loops/11-loop-event-predicate-trigger.md` — the direct structural precedent for "add a
   new reflex action kind" done right: real migration widening the CHECK/FK, a new `HookFunc` +
   `Executor` field + `case` branch, and an explicit rejection of a generic `callback` kind per
   `10-reflex-action-taxonomy.md`'s own ruling. Task `03` here follows the identical shape.
4. `docs/engineering/GLOSSARY.md` — checked for collisions at planning time: `run_python_sandbox`,
   `PythonToolDispatcher`, and `NewPythonToolDispatcher` are new names with no existing entry to
   collide with. One existing entry needs a small update, not a new entry: the **Reflexes** entry
   (line 39) enumerates the action kinds in prose ("injects a reminder, nudges a tool choice,
   sends a message, adds a schedule, dispatches to another agent... or halts a session") and will
   be stale-by-omission once `03` lands a seventh kind. Task `03` updates that sentence.
5. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer discipline, and
   escalation rules every task file below follows.

## Load-bearing corrections and design calls made this planning session

Found or decided during this session's own research against current code, not silently baked
into a task's Context alone — logged in full in `TASKS/ESCALATIONS.md`'s corresponding
2026-08-21 "Code Mode planning" entry.

1. **Correction to the outer planning brief: this batch DOES need a real schema migration.**
   The brief that scoped this batch assumed "very unlikely to need a schema migration at all...
   confirm this rather than assuming." Confirmed, and the assumption doesn't hold: since this
   doc was drafted, `TASKS/reflex-taxonomy/` has landed and is `reviewed` — `agent_reflexes.
   action_kind` is no longer a bare CHECK-constrained TEXT column, it carries both a widened
   CHECK list **and** a real `REFERENCES reflex_action_kinds(name)` FK (migration `124`), and a
   brand-new `reflex_action_kind_provenance_allow` join table gates which provenance tier may even
   *declare* a given kind (migration `125`, enforced live at write time by
   `internal/api/reflexes.go`'s `validateReflexDefinition` → `Store.ActionKindAllowsProvenanceTier`
   — a presence-based allow-list: zero rows for a kind means **no tier can ever declare it**, full
   stop). Task `03`'s `run_python_sandbox` action kind is therefore a real, if small, migration:
   one new `reflex_action_kinds` row, three new `reflex_action_kind_provenance_allow` rows (one
   per tier — see design call #4 below), and an `agent_reflexes` CHECK-widen rebuild identical in
   shape to migrations `119`/`124`'s own precedent. See task `03`'s own Context for the full DDL.
2. **Task `01`'s `PythonToolDispatcher` adapter needs a genuine new type, not a one-line
   assignment.** `PythonPermissionChecker` — the permission-check half — is satisfied directly by
   `container.Permissions` (`*permission.Engine`) with zero adapter code: its `Check(ctx,
   sessionID, toolName, input, meta) CheckResult` method already matches
   `selftools.PythonPermissionChecker`'s interface signature exactly, structurally. The dispatch
   half does not: `selftools.PythonToolDispatcher.Dispatch(ctx, sessionID, toolName, args) (any,
   error)` vs. `service.ToolService.Execute(ctx, agentID, toolName, input) (*ToolResult, error)`
   — different second parameter (agentID vs. sessionID) and a different return shape (`*ToolResult
   {Output string, IsError bool}` vs. `any`). A real, small adapter is needed — task `01` builds
   `service.NewPythonToolDispatcher(tools ToolService) selftools.PythonToolDispatcher`, resolving
   the agentID via `mcp.CallerProfileFromContext(ctx)` (the same ctx-stamping convention
   `chat_tool_executor.go` already establishes) rather than threading a second identity through
   the dispatcher interface itself.
3. **Task `01`'s adapter return-shape is a real, flagged design call, not a re-derivable fact.**
   `python_run`'s own tool description promises `tool_call()` "returns a dict, raises
   RuntimeError on denial or error" (`self_tools_python.go:143` in the Python preamble's own
   docstring), but most self-tool results are `mcp.TextResult`-wrapped plain prose, not JSON —
   there is no existing convention in this codebase for turning an arbitrary tool's
   `ToolResult.Output` into a Python-side dict. This planning session's default (task `01`'s own
   Context has the full reasoning): wrap every non-error result as `map[string]any{"output":
   result.Output}` — always a dict, as promised, with no fragile JSON-sniffing heuristic — and
   turn an `IsError` result into a Go `error` so it surfaces as a Python-side `RuntimeError`,
   matching the doc's own promise and `pumpToolCalls`'s existing error-status bookkeeping. Flagged
   as adjustable, not locked; the worker documents its own call if it deviates.
4. **Task `01`'s wiring and task `03`'s wiring are two independent construction sites, not one
   read-back of the other — a real, non-obvious object-graph constraint, not a stylistic
   choice.** `SelfToolsTransport` (task `01`'s target) is built entirely inside `cmd/nanite/
   main.go`'s `initMCP` (line ~997), *before* `service.NewContainer` is ever called (line ~372)
   — so by the time `container.Tools`/`container.Permissions` exist, `selfTools` already does,
   and the reverse read (`NewContainer` reading `selfTools`'s fields back) is structurally
   impossible without restructuring main.go's boot order, which is out of this batch's scope.
   Separately, `reflexEngine` (task `03`'s target) is built and wired entirely *inside*
   `internal/service/container.go`'s `NewContainer` (lines 950-973) — a completely different
   object graph that has no reference to `selfTools` at all. Both tasks therefore independently
   call `service.NewPythonToolDispatcher(...)` against their own locally-scoped `tools`/
   `permissions` values (main.go's `container.Tools`/`container.Permissions` for `01`; container.
   go's own local `tools`/`permissions` vars, already in scope before line 950, for `03`) —
   two separately-constructed but behaviorally-identical stateless adapter instances, not one
   shared object. This is fine (the adapter is a stateless wrapper) and is called out explicitly
   so a future reader doesn't "simplify" it into a single shared field and reintroduce a
   construction-order bug.
5. **Task `03`'s new action kind classification** (Facet 1/2/4 from `10-reflex-action-taxonomy.
   md`, needed for the new `reflex_action_kinds` row): **category = `execute_action`** (firing it
   runs a script directly, no LLM discretion involved — the taxonomy doc's own definition,
   matching `send_message`/`add_schedule`/`halt_session`/`dispatch_to_agent`, not
   `inject_reminder`/`force_tool_choice`'s `system_message` category). **combining_algorithm =
   `all_applicable`** — each `run_python_sandbox` firing is an independent, non-contradictory
   side-effecting computation (unlike `force_tool_choice`'s single-directive conflict or
   `halt_session`'s session-ending blast radius), the same reasoning that puts `send_message`/
   `add_schedule` at `all_applicable`. **default_recurrence_seconds = `NULL`** (inherit the system
   default cooldown) — unlike `dispatch_to_agent`'s deliberate `0`/no-cooldown override (a routing
   decision needs fresh per-turn evaluation), a periodic aggregation/computation has no equivalent
   "must re-evaluate every turn" requirement.
6. **Task `03`'s provenance-tier allow-list — all three tiers allowed, no restriction.** The
   taxonomy doc names `halt_session`/`dispatch_to_agent` as the two "obvious candidates" to
   restrict away from `plugin` tier specifically because of their blast radius (session
   termination, agent routing). `run_python_sandbox` doesn't share that shape — every `tool_call()`
   it makes is still fully permission-gated by task `01`'s fix, and the sandbox itself is
   resource/time/network capped regardless of who declared the reflex. Seeded with all 18→21
   pattern's newest 3 rows: `(run_python_sandbox, system)`, `(run_python_sandbox, operator)`,
   `(run_python_sandbox, plugin)` — all allowed. Adjustable later via a plain INSERT/DELETE against
   `reflex_action_kind_provenance_allow`, per migration `125`'s own stated precedent — not locked
   security policy.
7. **The session-id-minting question (task `02`), resolved: reuse the `WorkflowRun`'s own ID as
   the session id — do not mint a new synthetic one.** Investigated what `mcp.SessionIDFromContext`
   /`WithSessionID` actually key off downstream before deciding: (a) `permission.Engine.Check`'s
   session-scoped grants (`sessionGrants[sessionID][toolName]`) — any stable string works, no
   requirement that it resolve to a real `sessions` row; (b) `store.GetSession(sessionID)` lookups
   elsewhere (e.g. `resolveProjectIDFromSession`) degrade softly to `""` on a not-found ID, they
   don't hard-fail; (c) `event_log.session_id` (`internal/store/migrations/001_schema.sql:475`) is
   a bare `TEXT` column with **no FK constraint** — confirmed by reading the schema directly, not
   assumed. The codebase already has a live precedent for a non-FK'd, non-"real"-session sentinel
   string in exactly this code path: `callRunPython`'s own `"ptc-default"` fallback
   (`self_tools_python.go:2456`) when no session ID is available at all. Given all of that, minting
   a brand-new synthetic ID per `WorkflowRun` would add an extra ID↔run mapping for zero benefit;
   reusing `workflow_runs.id` directly (already available as `buildToolStepRequest`'s `runID`
   parameter, `workflow_engine.go:832`) gives every tool call from that run a stable identifier
   that's already meaningful and immediately correlatable everywhere else that run is referenced,
   with no realistic collision risk against real chat `sessions.id` values (independently-generated
   ID spaces). Task `02` implements this as `ExecuteToolStep`'s own fallback: use `req.SessionID`
   when the step author explicitly configured one (mirroring `buildLLMStepRequest`'s existing
   `configString(cfg, "session_id")` precedent, `workflow_engine.go:821`), else fall back to
   `req.WorkflowRunID`.
8. **`internal/api/reflexes.go`'s `validateReflexDefinition` has its own separate, hardcoded
   Go-side action-kind switch (lines 318-325) — distinct from the DB CHECK and distinct from the
   provenance-allow table.** Easy to miss: even with the migration and the provenance-allow rows
   in place, a `run_python_sandbox` reflex written through the CRUD API would still be rejected
   with `"invalid action_kind"` at this line unless the switch's case list also gets the new
   constant added. Task `03`'s What-to-do calls this out explicitly as its own edit, not folded
   silently into the migration bullet.

## What this batch does NOT do

- **Wire `run_python_sandbox` (or any Code Mode use) into `internal/scheduler`'s periodic-job
  surface.** Doc 27 names "durable periodic computation" as a proposed use case; this batch
  makes Code Mode reachable from a reflex trigger (predicate/event/interval) and from a workflow
  tool step — a scheduled periodic run is a `add_schedule` reflex or a `loop_run_tick`-style
  producer pointed at a `run_python_sandbox` reflex, composable from what this batch ships, not a
  fourth task this batch builds directly.
- **Feed a `run_python_sandbox` reflex's result back into LLM context** (e.g. as an injected
  `system_message`-category reminder). Task `03`'s hook logs the result and returns; richer
  feedback-into-context is a real follow-up if a concrete use case shows up, not assumed here —
  matches the taxonomy doc's own "start narrow" bias and keeps task `03`'s diff to the same small
  shape (`HookFunc` + `Executor` field + `case` branch) the brief that scoped this batch asked for.
- **Extend the same `mcp.WithSessionID`/`WithCallerProfile` stamping fix to `ExecuteLLMStep`.**
  `ExecuteLLMStep`'s own internal tool-call loop (`workflow_step_executor.go`, the `for iter :=
  0;;` loop calling `e.tools.Execute`) has the same structural gap task `02` fixes for
  `ExecuteToolStep` — neither doc 27 nor this batch's scoping brief names it, and it isn't a case
  of a stated rationale not holding (the brief's own text names `ExecuteToolStep` specifically,
  citing `chat_generate.go` as the only place threading `python_run`-relevant reflex/workflow
  reach). Flagged here as a related, deliberately out-of-scope observation for a future task, not
  a silent gap.
- **Sandbox hardening, resource-limit changes, or network-deny changes to `RunPythonSandbox`
  itself.** The sandbox mechanism (`internal/selftools/self_tools_python.go`) is confirmed correct
  by design per doc 27 — this batch fixes what's *wired to* it, not the sandbox's own internals.
- **A CRUD/admin UI for authoring `run_python_sandbox` reflexes.** Matches every prior batch's
  "no frontend work in any backend phase" fence — authored via the existing `agent_reflexes` API/
  CLI/seed paths, same as every other action kind.

## Task sequence

Flat-numbered `01`-`03`, one phase boundary in the middle (per the operator brief's own
sequencing: the wiring fix is the real, live bug and must land first; the two enablement pieces
are meaningless without it, but are mutually independent of each other).

**Phase 1 — Production wiring fix.** The actual, live security/correctness gap. No dependency on
anything else in this batch.

| Task | Depends on |
|---|---|
| `01-fix-python-sandbox-permission-and-dispatcher-wiring.md` | none |

**Phase 2 — Non-agentic reach.** Both build on `01`'s fix being real (a `python_run` call reaching
a still-unwired dispatcher fails the same way regardless of caller); neither touches the other's
files.

| Task | Depends on |
|---|---|
| `02-workflow-tool-step-session-stamping.md` | `01` (sequencing only — see below; no file overlap) |
| `03-run-python-sandbox-reflex-action-kind.md` | `01` (real: calls `service.NewPythonToolDispatcher`, built by `01`) |

`02`'s dependency on `01` is a sequencing choice, not a file/type dependency — `02` touches
`internal/agentworkflow/types.go`, `internal/service/workflow_step_executor.go`, and
`internal/service/workflow_engine.go` only, none of which `01` touches. It is ordered after `01`
here because a workflow-triggered `python_run` call is functionally meaningless (still hits the
same unwired-dispatcher failure) until `01` lands — matching the operator brief's own reasoning —
not because the code requires it.

## Parallelization plan

**Wave 1.** `01` alone.

**Wave 2 — parallel, worktree-isolated, once `01` lands.** `02` and `03`. Confirmed file-disjoint:
`02` touches `internal/agentworkflow/types.go`, `internal/service/workflow_step_executor.go`,
`internal/service/workflow_engine.go`. `03` touches `internal/store/agent_reflexes.go`,
`internal/agent/reflexes/executor.go`, a new `internal/store/migrations/*.sql` file, `internal/
api/reflexes.go`, `internal/service/container.go`, a new `internal/service/
reflex_python_sandbox_hook.go`, and `docs/engineering/GLOSSARY.md`. Zero overlap with `02`'s list.

## Migration numbering

> **⚠️ STALE CLAIM — annotated 2026-08-24 at `5ec930c8`. The `144` below is taken. Do not use
> it.** Every premise in the paragraph that follows has moved since 2026-08-21; it is kept as
> written so the correction is visible rather than silently applied.
>
> - `144` is **occupied** by `144_workflow_run_waiting_on_loop_status.sql`, landed by Loops.
> - Loops did not use `138`-`143`. A uniform +3 shift (`63d79028`) moved its range to
>   `138`-`146` once Skills landed `136`/`137` first.
> - Skills' `136`-`137` landed as `136_skills_index_redesign.sql` and
>   `137_agent_known_skills_grant_state_and_drop_agent_skills.sql`.
> - `TASKS/plugin-system`'s `135` was never taken, and **`135` must never be used now** — that
>   renumber left it as a hole below the highest applied version. Goose selects migrations by
>   version number alone: in `UpVersions` (`internal/gooseutil/resolve.go`, goose v3.27.3) the
>   applied set is a map keyed on the version integer, with no filename and no checksum, and
>   both selection loops skip any version already in it. So filling a hole does one of two
>   things, depending on whether the database in question has already applied that number:
>   **not applied** → collected as missing, and since `internal/store/store.go` builds its
>   provider without `WithAllowOutofOrder` the run fails with a missing-migration error, a hard
>   boot failure; **already applied** → both loops skip it, the file never runs, nothing is
>   reported, and goose considers the database up to date — **silent**. There is no third case,
>   since a number equal to the highest applied version is by construction already applied. The
>   silent branch is the dangerous one: schema divergence between databases of different
>   vintages, with no startup failure to announce it.
>
> **Next free is 148**, derived at `5ec930c8`:
>
> ```
> $ ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1
> 147_remove_untouched_official_catalog_source.sql
> ```
>
> **Re-derive it again immediately before task `03` writes its migration.** 148 is itself a
> hint that expires — several frozen batches resume at once, and the number is only claimed
> once the file exists on `main`. See `TASKS/INDEX.md`'s "Migration numbering — the claiming
> rule" banner and `docs/engineering/tracking-integrity.md` check 9.

Highest existing goose migration on disk at this planning session's authoring time (2026-08-21)
is `134_agent_profiles_protocol_transport.sql`. Three sibling batches currently sitting as
uncommitted/unstarted plans have already provisionally claimed numbers past that: `TASKS/
plugin-system` claims `135`; `TASKS/skills` claims `136`-`137`; `TASKS/loops` claims `138` onward
across its own six Phase-1 schema tasks (`138`-`143`). This batch has exactly one migration (task
`03`) and provisionally claims **`144`** — the next free slot after all three siblings' own
provisional ranges. Same real cross-batch collision risk every sibling README already flags:
**re-list `internal/store/migrations/` immediately before landing task `03`'s migration** and
renumber if any of `plugin-system`/`skills`/`loops` (or anything else) has landed a migration
first in the meantime.
