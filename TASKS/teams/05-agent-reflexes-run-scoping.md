# Run-scoped `agent_reflexes` — the design doc's own named "real gap," at both real call sites

**Phase:** 1 — Schema & storage foundation (`TASKS/teams`)
**Status:** not-started
**Depends on:** none directly (independent column addition; parallel-safe with `01`/`02`/`03`/`04` — coordinate migration numbering only, see `01`'s numbering note)
**Touches:** `internal/store/migrations/` (new migration — nullable `agent_reflexes.workflow_run_id` column), `internal/store/agent_reflexes.go`, `internal/agent/reflexes/` (candidate-set lookup feeding `Resolve()`), `internal/service/chat_reflex_dispatch.go` (`attemptReflexDispatch`), `internal/selftools/self_tools_dispatch.go` (`matchDispatchToAgentReflex`).

## Context

`docs/engineering/architecture/15-teams.md`'s "Routing: real reuse, and one real gap" section names this precisely: *"`agent_reflexes.agent_id` today is only `NULL` (class-bound/global) or a specific agent... there is no third scoping dimension for 'this reflex exists only for the lifetime of this run, and its candidate set is this run's resolved members.' That's genuine new schema/engine work... not configuration over what exists."* Confirmed directly against the real migration chain (through `119_agent_reflex_dispatch_to_agent.sql`, plus `124_reflex_action_taxonomy.sql`/`125_reflex_action_kind_provenance_allow.sql`'s later `ProvenanceTier`/`RecurrenceOverrideSeconds` additions) — the design doc's claim holds exactly.

**A real, non-obvious cost the design doc's single-paragraph description doesn't surface:** `dispatch_to_agent` evaluation happens at **two independent real call sites**, split by a genuine import-cycle constraint (`internal/selftools` cannot import `internal/service`) — `attemptReflexDispatch` (`internal/service/chat_reflex_dispatch.go`) and `matchDispatchToAgentReflex` (`internal/selftools/self_tools_dispatch.go`). Both intentionally run, not collapsed into one (explicit comment at `self_tools_dispatch.go` lines ~45-53 — `task_execute`'s own dispatch runs a second, independent evaluation downstream of the upstream one). **Both need the run-scoped candidate-set lookup** — fixing only one and assuming symmetry will produce a Team whose routing works from one entry point and silently doesn't from the other.

**The reflex-taxonomy work has already landed on `main`, ahead of what `15-teams.md`'s own text assumes is still informal** — a shared decision engine, `reflexes.Resolve(candidates, state)`, is called from all three real evaluation sites (the two above, plus `Engine.EvaluateState`'s generic per-turn pass). Build on `Resolve()` directly rather than reimplementing the candidate-list/evaluate/pick loop a third time. **Do not regress** `TASKS/reflex-taxonomy/09-fix-duplicate-kind-lookup-warn-logging.md`'s prior fix, which correctly excludes `dispatch_to_agent` rows from `Engine.EvaluateState`'s generic pass entirely (that pass has no live dispatch hook for this action kind) — a run-scoped row must stay excluded from that third site too, not newly leak into it.

**Combining algorithm** for `dispatch_to_agent` is `first_applicable` (highest-priority firer wins, ties broken by `created_at ASC`) — already the live algorithm, no new combining logic needed here; this task only widens the *candidate set* a run-scoped session sees, using the existing algorithm as-is.

**Provenance tier requirement, real, easy to miss:** migration `125`'s `reflex_action_kind_provenance_allow` table gates which `provenance_tier` values are valid for which `action_kind` — confirmed `(halt_session, plugin)` is explicitly absent/denied for at least one kind, and Team-authored routing rules are not operator-authored, so whatever `provenance_tier` this task's run-scoped rows (or task `09`'s, which actually creates them) carry must pass `ActionKindAllowsProvenanceTier` for `dispatch_to_agent` — confirm which tier is appropriate (`operator` is the safest default if Team routing rules are conceptually "configured by whoever created the Team," but document your actual reasoning) rather than assuming.

**Real seed example to model the shape against** (not hypothetical): `internal/agent/reflexes/seeds.go:334-352`, `dispatch_to_agent_open_subagent` — `ClassTag: "advisor"`, `TriggerKind: "predicate"`, `Priority: 10`, `ActionKind: "dispatch_to_agent"`, `ActionSpec: {"agent_slug":"planner","confidence":0.75,"reason":"..."}` (shape documented `internal/store/agent_reflexes.go:45-47`).

## What to do

1. New migration (confirm the actual next-available number at dispatch time — see `01`'s numbering note): add nullable `agent_reflexes.workflow_run_id TEXT REFERENCES workflow_runs(id)`. `NULL` = global/class-bound, exactly as today — this codebase's and `go-scheduler`'s existing convention for "unscoped." No table rebuild needed if a plain `ALTER TABLE ADD COLUMN` suffices for a nullable FK-referencing column with no CHECK — confirm SQLite allows this directly (it generally does for a nullable column with no new CHECK); if it doesn't in practice, use the standard rebuild pattern instead and document why.

2. **Candidate-set lookup**: find and extend whatever function currently loads `dispatch_to_agent` candidates feeding `Resolve()` at both call sites, so that when evaluation happens inside a session that is itself a `team_run_members`-resolved session (i.e., its `session_id` maps to a real `team_run_members` row — task `02`'s table), the candidate set includes `agent_reflexes` rows where `workflow_run_id` matches that run **or** `workflow_run_id IS NULL` (global rules still apply; run-scoped rules layer on top, don't replace). **Document the priority ordering you land on** when both a global and a run-scoped rule could fire for the same predicate — the design doc's routing section establishes semantic-rule-before-coordinator-fallback ordering explicitly but doesn't address global-vs-run-scoped precedence at all; your documented call here is real design work, not a mechanical translation.

3. Apply this at **both** `attemptReflexDispatch` and `matchDispatchToAgentReflex` — write one test per call site proving run-scoped isolation (a rule scoped to run A does not fire for a session in unrelated run B, or for a non-Team session at all).

4. Confirm `Engine.EvaluateState`'s existing `dispatch_to_agent` exclusion still holds unchanged after this column addition — add a regression test if one doesn't already directly cover "a `dispatch_to_agent` row with a non-NULL `workflow_run_id` is still excluded from the generic per-turn pass," since this is exactly the kind of column-addition-induced regression this project's history shows happens easily.

## Done means

- Migration applies cleanly against a real backup copy of the database, not just an empty fixture.
- Regression tests at **both** `attemptReflexDispatch` and `matchDispatchToAgentReflex` proving a run-scoped rule fires only for sessions belonging to its own run and is invisible elsewhere; a global (`workflow_run_id IS NULL`) rule is unaffected and still fires everywhere it did before.
- Documented, tested global-vs-run-scoped priority ordering.
- Documented `provenance_tier` choice, confirmed via a direct test that it passes `ActionKindAllowsProvenanceTier` for `dispatch_to_agent`.
- `Engine.EvaluateState`'s dispatch_to_agent exclusion confirmed unregressed. `go build`/`vet`/`test` clean.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
