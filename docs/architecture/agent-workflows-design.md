# Agent Workflows — Design

Status: design, ready for implementation. Internal engineering name: **Workflow** (the fifth architectural pillar, alongside GUI/Harness/Launching/Agents — see `docs/research/five-pillars-and-agent-workflows.md` for the full reasoning trail that led here). Branding candidate: **Agent Workflows**.

Implements Torque task `CW-20260813-0001`.

## What this is for

Without it, the harness always trusts an LLM's own judgment about whether to call a tool, in what order, and whether its self-reported outcome is true. That's fine for open-ended chat. It breaks down for the "many agents with specific, rigid roles in a pipeline" pattern — the motivating failure was an agent asked to fetch tasks from Torque instead fabricating them, because nothing made the fetch mandatory or checked the claim.

Workflow adds two capabilities, kept deliberately separate because they're different problems:

1. **Deterministic sequencing** — force *which* step happens *when*, regardless of what an LLM would choose on its own.
2. **Trustworthy completion** — don't trust what a step *says* it did; a step can be sequenced correctly and still lie about its result.

Constraints are opt-in per workflow. A workflow with no constraints degrades to exactly today's open-ended chat behavior. This is additive, not a replacement.

## The one principle everything else follows from

**Workflow engines only decide sequencing. Every unit of work — every LLM turn, every tool call, every verification check — always executes through Nanite's existing harness, never through the engine itself.**

This holds for the built-in engine and for external engines (LangGraph, CrewAI) identically. Skipping this means running a second, parallel harness inside whatever external framework is in use — bypassing the tool broker, permission engine, Tesseract memory integration, and envelope system — which quietly defeats the point of this pillar. An engine's job is to decide what runs next. Nanite's harness always decides how anything actually runs.

## Adapter interfaces

```go
type WorkflowEngine interface {
    Name() string
    Run(ctx context.Context, wf WorkflowDefinition, input WorkflowInput, exec StepExecutor) (WorkflowResult, error)
}

type StepExecutor interface {
    ExecuteLLMStep(ctx context.Context, req LLMStepRequest) (LLMStepResult, error)
    ExecuteToolStep(ctx context.Context, req ToolStepRequest) (ToolStepResult, error)
    Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error)
}
```

Mirrors `Dependencies.ProviderAdapter` from Launching — a thin resolver over per-backend implementations, not a new architectural pattern for this codebase.

**`StepExecutor`'s real implementation lives in `internal/service` (a `internal/service/workflow*.go` area), as plain functions — not in the MCP layer, not duplicated per consumer.** This follows the same pattern already proven three times over: `harness_v1`'s HTTP handlers, the MCP self-tools, and `nanite chat`'s CLI client are all thin wrappers over `internal/service`. Workflow's callback surface is a fourth thin-wrapper consumer of the same service layer, not a new implementation.

Consequence worth being deliberate about: the built-in engine calls these functions **directly, in-process** — no wrapper needed, same binary. External engines reach the same functions through thin MCP-tool wrappers (below). This is what makes "engines only sequence, harness always executes" a provable guarantee rather than a design intention two implementations might quietly drift apart from — the built-in engine and an MCP-driven LangGraph node run identical code for step execution, not code that merely agrees today.

## How external engines (LangGraph, CrewAI) integrate

No new protocol. Nanite already runs itself as an MCP server for CLI-launched agents (`internal/mcpserver`). Add three new MCP tools there — `workflow_execute_llm_step`, `workflow_execute_tool_step`, `workflow_verify_step` — thin wrappers over the `internal/service` functions above. Launch the Python runner (LangGraph or CrewAI) as a subprocess with the same `.mcp.json`-pointing-back-at-Nanite mechanism already used for CLI agents. Both frameworks have native MCP client support (`langchain-mcp-adapters`, `crewai-tools`), so the Python side calls Nanite's tools directly — no custom RPC code needed on either side.

Lifecycle is one-shot, matching the existing `ModeOneShot` runtime-kind vocabulary: Nanite passes the workflow input at launch, the Python process runs the graph/crew to completion (calling back via the three MCP tools at each node that needs real work done), reports the result, exits.

Do **not** reuse `internal/runtime/agent.Boot` for spawning these — that package is scoped to CLI coding agents specifically (CLAUDE.md injection, agent-shaped boot directories) and a workflow runner isn't that. This needs its own, smaller subprocess-launch path.

Because the callback surface is MCP tools that are themselves thin wrappers over `internal/service`, adding an HTTP-based callback path later (for a consumer that isn't MCP-native) is a small addition on top of the same functions, not a redesign. Deliberately out of scope for this pass — MCP covers the near-term need.

## Built-in engine

**Scope: DAG only, no cycles.** Matches Hadron's own scope. LangGraph is the deliberate escape hatch for workflows that need a loop — don't build cycle support twice.

**Three step kinds:**
- `llm` — a capability-restricted agent turn. Give it only the tools appropriate to its role (Torque's pattern — e.g. a worker step gets no tool that lets it claim a terminal "done" state; that stays reserved for a reviewer step or the workflow's own completion logic).
- `tool` — an engine-owned MCP/HTTP call. The engine invokes it directly; an LLM never sees or chooses this call. The literal result feeds forward to dependent steps (Hadron's pattern).
- `gate` — human-in-the-loop pause (Hadron's `human_gate` / Torque's checkpoint shape).

**`verify` is a modifier on `llm`/`tool` steps, not a fourth step kind** — e.g. `verify: {mode: engine|agent, ...}` attached directly to the step it checks. This structurally prevents the failure mode of a workflow author wiring a separate verify step to the wrong predecessor (or forgetting to wire it at all). `mode: agent` spawns a second, independent `llm` step as reviewer — Torque's reviewer-end-agent pattern, expressed as an ordinary composed step rather than special-cased engine logic. `mode: engine` runs a deterministic check (the equivalent of Torque's git-commit/tool-activity cross-check) — the specific checks available start narrow and grow as real workflows need them; don't try to anticipate every possible check up front.

**Typed, persisted state.** Each step's output lands in DB-backed tables (`workflow_runs`, `workflow_run_steps`) keyed by step name, not log-line scraping — fixes Hadron's two biggest gaps (no typed inter-step state, not crash-durable) at the grain that actually matters for this use case (per-step, not per-blueprint). A crash/restart can resume from the last completed step.

**Level-based parallel execution** for steps whose dependencies are already satisfied — reusing Hadron's real, working idea (topological leveling + goroutine fan-out) at the right granularity this time (per-step, not per-blueprint-as-a-whole-node).

**Validation that the primitives are right, not something to build separately:** this step vocabulary is expressive enough to reproduce Torque's full three-layer trust model as an ordinary composed workflow — `llm` (capability-restricted worker) with `verify: {mode: agent}` (spawns an independent reviewer `llm` step) with an optional `gate` fallback if the reviewer can't resolve it. Nothing special-cased.

## POC scope — LangGraph and CrewAI

Hand-write a native graph (LangGraph) and a native crew (CrewAI) in Python, using each framework's own authoring API, whose nodes/tasks call the three MCP tools instead of a model client directly.

**Explicitly not building a compiler** that takes Nanite's own workflow-definition format and emits LangGraph-native or CrewAI-native structures. The two frameworks don't share a graph model — LangGraph is an explicit node/edge/state-channel graph, CrewAI is a role/task/process model — and attempting a universal DSL now, before real usage has shown which patterns are worth generalizing, is premature. The POC's job is to validate the callback mechanism (can an external framework's own native graph reuse Nanite's harness for every unit of real work), not to prove out a universal authoring format. Revisit if real demand for one shows up later.

## Integration with the rest of Nanite

- **A workflow run is a `template`-class durable agent**, not a new parallel tracking system. `template`'s existing definition — "boot → run → stop task execution" — already matches a one-shot workflow run. Reuse the existing durable-agent instance/event-log machinery (`internal/service/durable_agents.go`) rather than building a second one.
- **New `workflow_run` MCP self-tool.** Gives Chat's dispatch a new outcome alongside spawning a bare Worker/Planner — `classify.Classify`/`AssignRole` can route a task to a named, defined workflow when the task matches a rigid, repeatable process, instead of always spawning a single freeform agent.

## Explicit non-goals for this pass

- No cycles/loops in the built-in engine — use LangGraph for that.
- No universal workflow-definition-to-multiple-backends compiler.
- No HTTP (or other) callback surface for external engines yet — MCP only; the thin-wrapper-over-`internal/service` shape makes adding one later cheap, but building it now is premature.
- No attempt to anticipate every possible `verify: {mode: engine}` check kind up front — start with what real workflows need first.

## Prior art this design draws from

- **Hadron** (`apps/hadron`) — engine-owned `http_call`/`mcp_call` steps (the sequencing primitive), and its real gaps (no step-level DAG, no cycles, no typed state, not crash-durable, zero guardrails on its one LLM-driven step kind) — all addressed above at the step grain Hadron didn't operate at.
- **Torque** (`apps/torque`) — capability-restriction over claim-checking (the trust primitive), engine-owned independent verification, and the independent-second-agent-audit pattern — all expressed here as composable step properties rather than bespoke mechanisms.
- **Launching** (`internal/runtime/agent`) — the adapter-resolver shape (`Dependencies.ProviderAdapter`) this design mirrors for `WorkflowEngine`.
