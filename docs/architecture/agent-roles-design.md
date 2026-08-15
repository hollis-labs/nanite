# Agent Roles — Architect, Orchestrator, Planner, Project Manager, Worker, Reviewer

Status: design, ready for implementation. Scope: **Nanite only**, using Torque as the task-tracking system of record — no changes to Torque itself. This formalizes and extends work already largely proven out in practice, it does not start from zero.

## What this is for

We've been running something close to this pattern manually all session: an Architect role (design discussion → design doc → dependency-ordered Torque tasks with embedded boot prompts), dispatched work, and review passes. The goal now is to formalize it as real, reusable Nanite durable-agent recipes and a shipped Agent Workflow, so it can run without a human doing the Architect's ticket-writing and an Orchestrator's dispatch-and-poll loop by hand every time — and, deliberately, to validate that Nanite's own Agent Workflows/Durable Agents substrate (built this session) holds up on real, useful work before extending it further (external-framework validation, broader rollout).

## The one principle

**Formalize what's proven; don't rebuild what Torque already solved.** Torque ships its own Orchestrator/Planner/Worker/Reviewer system today (`internal/launchprofile/builtin.go`'s five builtin launch profiles), live and running in production — including a `reviewer.code` session that read, disposition'd, and merged a real PR today, and `implementer-long` Worker sessions that ran against this very repo today. That system dispatches **Torque-native CLI coding agents** and is not being replaced or duplicated here.

Nanite's five/six roles are a **distinct execution substrate** — Nanite durable agents and Agent Workflow runs — that uses Torque purely as the tracking system (task IDs, dependencies, status, checkpoints), reached only through the `torque_*` MCP tools, the same arm's-length relationship this entire session has already used successfully. No runtime dependency is created between the two apps; the portfolio invariant (apps share libraries only, never live cross-app runtime deps) holds unchanged. `internal/launchprofile/launchprofile.go`'s own doc comment is explicit that Torque deliberately does not bolt Nanite into its launcher — this design doesn't try to change that from Nanite's side either.

## Current state (what exists vs. what's new)

| Role | LifecycleClass | Status |
|---|---|---|
| Architect | `advisor` | **Exists, audited** — `architect-advisor` recipe (`durable_agent_recipes.go`). Tool-access audit done (`CW-20260815-0007`) — see findings below. |
| Planner | `template` | New. |
| Project Manager | `advisor` | **Exists informally** — `.nanite/agents/agridd-project-manager.md`, a real, currently-operating durable agent, scoped as a one-off repo file rather than a reusable recipe. Needs promotion. |
| Orchestrator | `harness` | New. |
| Worker | `template` | **Exists** — `template-worker`/`task-writer` recipes. The real lever is running Worker steps *through* Agent Workflows (`llm`/`tool` steps), not a new recipe. |
| Reviewer | `template` | **Proven but unshipped** — the exact Worker→Reviewer→Gate shape already exists as a Go test fixture (`internal/agentworkflow/definition_yaml_test.go`), never promoted to a real `WorkflowDefinition`. A standalone (non-workflow) Reviewer recipe is new. |

## Role definitions

### Architect (`advisor`, existing — `architect-advisor`)
What this session has been doing: system-design partner, produces design docs and dependency-ordered Torque tasks with embedded boot prompts. Doesn't write code or execute tasks itself. Needs full Torque task-lifecycle tool access (create/update/search, `depends_on` wiring) to actually do this — that's the audit item, not new capability.

**Audit findings (`CW-20260815-0007`, verified 2026-08-15).** `architect-advisor`'s pairing to `internal/agent/builtin/profiles/system-architect.md` is convention-only, not code-enforced — `ProfileRule: "operator_selected"`, no code strips a recipe-ID suffix to look up a matching profile slug. This is the same pattern every other recipe in this file uses (not a gap introduced by this audit). The profile's tool allowlist was extended with the four required Torque lifecycle tools (`torque_task_create`, `torque_task_update`, `torque_task_search`, `torque_task_get`) plus `dev_read`/`dev_write`/`dev_edit` for design-doc authoring. The "does not write code or run tasks" framing is preserved as an instruction-level boundary (the new write tools are explicitly scoped to design docs only, not source), not a tool-surface one.

### Planner (`template`, new)
One-shot sequencing pass, mirroring Torque's own `planner.default` framing ("short-lived plan refinement pass before the orchestrator walks phases"). Given a scoped goal or an Architect's design doc, produces a dependency-ordered set of Torque tasks with embedded boot prompts, following the "Task-authoring conventions" section below. This is exactly the mechanical part of what the Architect role has been doing by hand each time in this session — Planner is where that gets formalized into a repeatable, one-shot recipe.

### Project Manager (`advisor`, promote from `.nanite/agents/agridd-project-manager.md`)
Ongoing, advisory. Monitors live Torque task/dependency state, surfaces blockers and stalls, recommends dispatch timing and re-sequencing to a human or to the Architect — **coordinates, does not execute or dispatch**. This is the boundary that matters: PM talks about the plan's health; it never calls `subagent_spawn`/`workflow_run` itself. Promote the existing file into a real builtin recipe in `durable_agent_recipes.go` so it's reusable beyond this one workspace, rather than writing a new role from scratch.

### Orchestrator (`harness`, new)
Long-lived, executive. Reads Torque task state and dispatches ready work — **directly mirrors Torque's own Orchestrator design**, including its single hardest-won lesson: task `status` (via `torque_task_get`) is the only canonical liveness signal; session/process state is explicitly the wrong thing to poll (Torque's own orchestrator prompt calls this "FORBIDDEN" for exactly this reason — a live mid-tool-call session can transiently show absent `ExitCode`/`EndedAt` and a zero `PID`). For each ready task, dispatches via `workflow_run` (when the work matches a shipped `WorkflowDefinition`) or `subagent_spawn` (freeform). Waits on Reviewer/gate clearance before advancing dependents — same shape as Torque's own orchestrator waiting on its Reviewer.

### Worker (`template`, existing recipes — new usage pattern)
No new recipe required to start. The real change: route Worker execution through Agent Workflows `llm`/`tool` steps (capability-restricted, verified) rather than always a bare freeform `subagent_spawn` — this is the actual value Agent Workflows adds over a plain subagent, and this is where it should show up in practice.

### Reviewer (`template`, two shapes)
1. **Workflow-embedded** — a `verify: {mode: agent}` modifier on the Worker step it's reviewing. This is the shape already proven in `definition_yaml_test.go`; ship it as a real `WorkflowDefinition` (see below).
2. **Standalone** — a `reviewer` durable-agent recipe for reviewing freeform (non-workflow) Worker output, mirroring Torque's `reviewer.code`/`reviewer-end-agent` shape (reads the work product, checks acceptance criteria — matches the existing `reviewer.md` agent profile's "acceptance-criteria-driven" framing, as distinct from `code-auditor`'s rubric-driven review) and its checkpoint-escalation pattern on findings.

## The Worker → Reviewer → Gate workflow (the core dogfooding artifact)

Promote `definition_yaml_test.go`'s fixture into a real, shipped `WorkflowDefinition` YAML: a `tool`/`llm` Worker step with `verify: {mode: agent}` (spawning an independent Reviewer step), followed by a `gate` step for final approval. This is the single most direct piece of validation available — the shape is already implemented and tested at the engine level; what's missing is a real definition file and `internal/config`'s `WorkflowDefinitionsPath` actually being set (currently unset by default, registry loads nothing). Land this and the Orchestrator has a real workflow to dispatch Worker+Reviewer+Gate through, exercising the built-in engine's DAG/parallel execution on genuine multi-role work — a capability Torque's own team is still building toward (`SP-20260512-0004`, active, all five tasks still `todo`).

## Task-authoring conventions (for Planner and Architect)

Codifying what's worked in practice this session, so Planner doesn't have to rediscover it:
- Every task gets a `## Boot prompt` section embedded directly in the Torque task description — never relayed only in chat. `torque_task_get` alone must be enough for a fresh worker to start.
- Structure: `## What` (the problem, with file:line evidence where applicable) → `## How to fix` (concrete steps) → `## Non-goals` (explicit scope fences) → `## Boot prompt`.
- Use real `depends_on` chains only for genuine build-order dependencies; leave independent tasks undependent so they can run in parallel.
- When multiple tasks touch the same file(s) but have no real ordering dependency, flag the merge-conflict risk explicitly in each task's boot prompt rather than forcing an artificial dependency — suggest worktree isolation if run concurrently.
- Group related findings into one task when they share root cause or file scope, to preserve a worker's context; split into separate tasks when they're independently reviewable/parallelizable.
- Every review pass after a batch completes should be evidence-based (agents verifying real code/tests against the ticket's own claims and the relevant design doc), not commit-message trust.

## Explicit non-goals for this pass

- Not changing anything in Torque — its own Orchestrator/Planner/Worker/Reviewer system is untouched and continues serving Torque-native CLI coding-agent dispatch exactly as today.
- Not creating any runtime dependency between Nanite and Torque — all coordination is via the existing `torque_*` MCP tools.
- Not validating against external agent frameworks (LangGraph/CrewAI/etc.) in this pass — that comes after a real dogfooding run on the built-in engine proves the shape out, per the already-established sequencing (built-in first, external engines validate second).
- Not building new tooling for "compose a Torque task with a good boot prompt" — the gap is procedural/prompt-level (see conventions above), not a missing deterministic tool; `torque_task_create` already does everything needed.

## Prior art

- **Torque's own shipped Orchestrator/Planner/Worker/Reviewer system** (`internal/launchprofile`, `internal/orchestrator`, `internal/planner`, `internal/runtime/scheduler/end_agent.go`) — the most direct prior art available, live and validated in production. The polling-discipline lesson (task status only, never session/process state) is adopted directly, not reinvented.
- **`architect-advisor`, `template-worker`, `task-writer`** (`internal/service/durable_agent_recipes.go`) — existing recipes this design builds on rather than replaces.
- **`.nanite/agents/agridd-project-manager.md`** — the real, currently-running PM precedent being promoted into a reusable recipe.
- **`internal/agentworkflow/definition_yaml_test.go`** — the already-proven Worker→Reviewer→Gate shape this design ships as a real definition.
- **Agent Workflows** (`docs/architecture/agent-workflows-design.md`) — the execution substrate every role's structured work routes through; this design doesn't add a new execution mechanism, it uses the one already built.
