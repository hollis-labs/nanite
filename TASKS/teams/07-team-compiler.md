# The Team compiler — Team definition → `WorkflowDefinition`, the design's own guardrail piece

**Phase:** 2 — Runtime engine (`TASKS/teams`)
**Status:** not-started
**Depends on:** `01` (Team definition + phase sub-structure), `02` (`team_run_members` shape), `03` (`StepKindFlex` exists), `04` (authority grants — referenced, not enforced, by the compiled definition), `05` (run-scoped reflex routing — installed alongside compilation), `06` (flex-step semantics the compiled `Config` must satisfy)
**Touches:** `internal/service/team_compiler.go` (new).

## Context

This task builds the literal center of `docs/engineering/architecture/15-teams.md`'s "Guardrail" section — quoted in full because every line of this task exists to protect it:

```
Team definition
    ↓ resolve slots
    ↓ install run-scoped routing/reflex policy
    ↓ compile phase/gate sequence into a WorkflowDefinition
    ↓ launch an ordinary WorkflowRun
    ↓ existing agents + messaging + lifecycle do the actual work
```

*"Protect that framing during implementation. If building this starts to require a `TeamExecutionEngine`, `TeamMessageBus`, `TeamScheduler`, `TeamMemory`, or `TeamGateManager` — anything that duplicates a subsystem this doc already named as reused — that's a signal the design has drifted from what's written here, not a natural extension of it."* This task **is** the "compile phase/gate sequence into a `WorkflowDefinition`" step in that diagram — nothing more. Slot resolution (the step before it) is task `08`'s job; launching the resulting `WorkflowRun` (the step after it) is also task `08`'s job, via the existing `WorkflowLauncher`. This task's function should be callable in isolation, given already-resolved inputs, and produce a plain `agentworkflow.WorkflowDefinition` value — no side effects, no I/O beyond what's needed to read the Team definition itself.

**Forward-compat requirement, from the design doc's own explicit instruction** (its "What this session did not decide" list, third bullet): *"Implementation should keep the compile-Team-phase-sequence-into-WorkflowDefinition step as one clean function/module boundary with the phase sequence treated as its own addressable sub-structure within the Team definition, even though authored together for v1 — so a later split is 'accept a phase sequence from a second source' rather than a rewrite of the compiler itself."* Concretely: this function's signature should take the phase sequence as its own distinct input (task `01`'s `phases_json` sub-structure), not the whole `Team` blob undifferentiated — so a future "one Team runs different phase-sequence shapes" split is a call-site change, not a rewrite.

**Confirmed real target shape** (`internal/agentworkflow/types.go:270-293`): `WorkflowDefinition{Name string; Engine string; Steps []StepDefinition}`. `Engine` selects `EngineBuiltin` (the default, consumes `Steps`) versus an external engine (LangGraph/CrewAI/GoogleADK/AutoGen/LangChain via `internal/workflowrunner`, which ignores `Steps` entirely). **A compiled Team definition must always set `Engine` to empty/`EngineBuiltin`** — routing a Team through an external engine would be meaningless, since none of them execute `Steps` at all; make this a hard, not-configurable choice in the compiler, not an option a caller could accidentally get wrong.

**The design doc's own illustrative compile example** (`15-teams.md`'s "Illustrative shape" section) is the concrete acceptance target:
```
scope_work (flex, active_slots: [orchestrator, engineer, architect], exit_trigger: self_tool)
  → review_gate (gate, approver_slot: reviewer)
    → address_feedback (flex, active_slots: [engineer, reviewer, architect], exit_trigger: event)
      → merge_gate (gate, approver_slot: operator — human, not a Team Slot)
```

## What to do

1. `func CompileTeam(phases []store.TeamPhase, resolvedMembers map[string][]store.TeamRunMember) (agentworkflow.WorkflowDefinition, error)` (exact signature is your call — the key constraint is that phases and resolved-member data are separate, explicit inputs, not one opaque blob) in `internal/service/team_compiler.go`.

2. Walk the phase sequence, emitting one `StepDefinition` per phase: `kind: gate` for a gate phase (`Config: {approver_slot: ...}`, matching `StepKindGate`'s existing real config shape — confirm it against `internal/service/workflow_engine.go`'s actual gate handling rather than assuming the design doc's illustrative YAML is the literal Go `Config` shape), `kind: flex` for a flex phase (`Config: {active_slots: [...], exit_trigger: {...}}`, per task `06`'s real implementation). Wire `DependsOn` in sequence order, matching the illustrative example's linear chain — a Team's phase sequence is not assumed to be non-linear in this v1, document if you find a reason it needs to be.

3. Thread resolved member identity into a flex step's `Config.active_slots`: decide and document precisely whether this holds slot *names* (resolved again against `team_run_members` at flex-step-entry time, by task `06`'s executor) or already-resolved concrete `(agent_id, session_id)` tuples baked in at compile time. Recommend slot names only, resolved live at flex-step entry — keeps the compiled `WorkflowDefinition` reusable/inspectable independent of a specific run's member churn (a `replaced`-status member from `02`'s status vocabulary shouldn't require recompiling the whole definition) — but this is a real design call, document your reasoning either way.

4. Explicitly support a **fully fluid Team** — the design doc states this is a legitimate, gate-free shape (*"A Team is not required to declare any gates at all — a fully fluid team is a legitimate, gate-free shape"*): a phase sequence of one or more flex steps with no gate steps at all must compile correctly.

## Done means

- Unit test compiling the SME example above into a `WorkflowDefinition` matching it exactly (`kind`s, `DependsOn` chain, `Config` keys present and correctly typed) — a direct, literal reproduction of the design doc's own illustrative shape, not just "something reasonable."
- Unit test compiling a fully fluid (gate-free) Team, confirming it produces a valid `WorkflowDefinition` with only `flex` steps.
- Unit test confirming the compiled definition always has `Engine` set to the builtin default, never left to a caller-suppliable value.
- This task performs no store I/O beyond reading its inputs and no slot resolution — a pure(ish) function, testable without a live database. `go build`/`vet`/`test` clean.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
