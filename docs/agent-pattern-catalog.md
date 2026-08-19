# Agent Pattern Catalog

**Source ticket:** CW-20260426-0016 (M2 — special agent pattern catalog)
**Date:** 2026-04-26
**Complements:** `docs/agent-role-prompt-catalog.md` (M1 audit)
**Authority spec:** `internal/dispatch/doc.go` (three-role model); `internal/dispatch/role.go` (AssignRole map); `internal/classify/scope.go` (ScopeTier + ExecutionPattern vocabulary)

---

## How to read this catalog

Each pattern entry is a **template** — a named behavioral contract the harness can instantiate as an agent profile. Patterns do not map 1:1 to agent YAML files; a single agent may embody one pattern, and one pattern may be instantiated as many agents (e.g., a Worker pattern can produce `nanite-backend`, `nanite-frontend`, `nanite-reviewer-backend`).

The **ScopeTier hint mapping** section of each pattern describes which classifier outputs *should* route to this pattern when `AssignRole` is the decision-maker. The M3 reflex catalog wires the dispatch; the entries here describe the contract it honors.

**Status (post `TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`):** the M3 reflex layer is no longer the in-memory `internal/promptrouter` package this doc originally described (retired in full) — it is the DB-backed `dispatch_to_agent` reflex rows in `internal/agent/reflexes/seeds.go`. Of the patterns below, **Planner, Researcher, Reviewer, and Worker** have a real, live `dispatch_to_agent` reflex routing to them (plus Planner's pre-existing `AssignRole` tier/pattern routing). **Strategist and Documentor do not** — both are real pattern definitions with no matching agent profile in this codebase, so their entries below describe an aspirational routing contract only ("what *should* happen if a profile existed"), not something reachable today. See the migration task's Work Log for the phantom-entry accounting.

---

## 1. Chat — Harness Dispatcher

**Name:** Chat
**One-line purpose:** Primary user-facing agent that dispatches all execution to Worker or Planner; never does work directly.
**When this is the right fit:** Any session slot designated as the user-visible interface; the agent the user types into.

### Tool surface

**Needs:**
- `nanite_todo_*` — task tracking primitive
- `nanite_plan_*` — plan management primitive
- `nanite_scratchpad_*` — per-turn key/value buffer
- `nanite_message_*`, `nanite_handoff_*` — peer messaging / handoff
- `nanite_show_*` — envelope narration (render cards Chat received from Workers)
- `nanite_execute_task` — the dispatch primitive itself
- `fetch_tool_result`, `search_tool_result`, `request_tools` — meta-tools (always allowed; exempt from surface filter)

**Explicitly does NOT need (and must be blocked at boot):**
- `dev_read`, `dev_write`, `dev_edit`, `dev_glob`, `dev_grep`
- `shell_exec`, `bash`, `web_fetch`, `web_search`
- `mcp__*` (all external MCP tools)
- `nanite_spawn_subagent`, `nanite_subagent_status`, `nanite_subagent_cancel` (raw spawn management)

Surface enforcement is implemented in `dispatch.EnforceChatSurface` and `dispatch.IsChatSurfaceTool`.

### Prompt anchor

```
You are the Nanite chat harness — the user-facing layer of a three-role agent system.
Your job is to understand intent, track plans, and dispatch execution via nanite_execute_task.
You do NOT run tools, write code, or call external services directly.
When a task needs work done: executeTask → Worker or Planner receives it → you receive the result envelope.
Render the result envelope; do not re-execute or verify it by running tools yourself.
Ask one pointed question when scope is unclear. Confirm before any destructive or externally-visible action.
```

### ScopeTier hint mapping

The Chat role is not dispatched — it is the dispatch initiator. The AssignRole contract does not apply to Chat directly. Detection is via prompt template slug: agents bearing `chat-role-harness` (seeded by migration 027, ID `blt-chat-harness-001`) have their tool surface clamped to `ChatToolSurface` at boot. No ScopeTier routing to Chat; it is always the session-entry agent.

### Lifecycle

**Lifecycle:** Session-bound. One Chat agent per session; static, never spawned mid-turn.
**Ephemeral or persistent:** Persistent for the session duration.

### Composition

Chat is the root of the composition tree. It dispatches to Worker (for bounded execution) or Planner (for open-scope tasks). It receives result envelopes from dispatched agents and renders them via `nanite_show_*`. It does not delegate to other Chat instances. Does not compose laterally; only down-dispatches.

### Anti-patterns

- **Giving Chat execution tools** — if Chat can `dev_read` or `shell_exec`, the surface boundary collapses and context-pollution follows. The harness spec prohibits this explicitly.
- **Having Chat verify Worker output by re-running tools** — Chat receives the result envelope; re-running tools leaks execution responsibility back into Chat.
- **Multiple Chat agents in one session** — Chat is session-unique. Using two Chat agents creates conflicting dispatch surfaces.
- **Using Chat for background work** — background tasks should be dispatched to a Worker with `PatternBackground`; Chat itself is interactive and synchronous.

---

## 2. Strategist — Analysis Without Execution

**Name:** Strategist
**One-line purpose:** Articulate tradeoffs, frame decisions, and produce analysis; no code, no execution, no plan decomposition.
**When this is the right fit:** When the question is "what should we do and why" rather than "how do we do it." Useful before a Planner session (to frame the decision space) or after a Reviewer session (to interpret findings).

### Tool surface

**Needs:**
- `nanite_show_document`, `nanite_show_report` — structured output rendering
- `nanite_scratchpad_*` — working memory within a turn
- `nanite_message_*` — receive task context, send analysis results back to dispatcher
- `fetch_tool_result`, `search_tool_result` — meta-tools
- Read-only knowledge access: `doc-search`, `mcp__conduit__*` (read), KB oracle if configured

**Explicitly does NOT need:**
- Any write, edit, or execute tool (`dev_write`, `dev_edit`, `shell_exec`, `mcp__engine__task_create`)
- `nanite_execute_task` — Strategist does not dispatch further agents
- `nanite_todo_*`, `nanite_plan_*` — task/plan management belongs to Planner or Chat

### Prompt anchor

```
You are a Strategist. Your output is analysis, tradeoff articulation, and decision framing.
You do not write code, execute tasks, or decompose work into plans.
Read the context given to you; synthesize it into a clear position with explicit alternatives.
When you identify a decision point, name the options, their costs, and your recommendation.
Surface assumptions and risks. Stop when the decision space is clear — do not over-elaborate.
Do not initiate follow-up work; return your analysis to the dispatcher.
```

### ScopeTier hint mapping

The Strategist pattern is not a direct dispatch target in the current `AssignRole` map (which maps to Worker or Planner only). M3 may route to a Strategist-profile Worker for `TierSmall × PatternInline` or `TierMedium × PatternSubagent` when the task classifier detects an analysis-only intent signal. The Strategist profile is a read-only Worker specialization — its ScopeTier range is `trivial` through `medium`; tasks classified as `large` or `open` should go to Planner (for decomposition) or Worker (for execution), not Strategist.

### Lifecycle

**Lifecycle:** Subagent (dispatched by Chat or Planner); single-shot.
**Ephemeral or persistent:** Ephemeral — one task in, analysis out, session ends.

### Composition

Can receive task context from Chat or Planner. Returns a structured analysis envelope. Does not dispatch further agents. Can be composed before Planner (to frame work) or after Reviewer (to interpret findings). Strategist output is an input to Planner decomposition, not a replacement for it.

### Anti-patterns

- **Giving Strategist write tools** — the moment it can act on its own analysis, it becomes a Worker. Keep the surfaces separate.
- **Using Strategist for task decomposition** — that is Planner's job. Strategist says "here are the options"; Planner says "here is the sequence of steps."
- **Routing large or open-scope tasks to Strategist** — open-scope tasks need decomposition (Planner), not just analysis.
- **Session-persistent Strategist** — Strategist should not accumulate state across tasks; each dispatch is independent.

---

## 3. Planner — Decompose and Sequence

**Name:** Planner
**One-line purpose:** Break a task into structured, sequenced sub-tasks with estimates; produce a plan, not execution.
**When this is the right fit:** When the task is too large or ambiguous to hand directly to a Worker — scope is `TierOpen`, the shape of work is unclear, or parallel execution needs coordinating.

### Tool surface

**Needs:**
- `nanite_plan_*` — plan creation and management (core primitive)
- `nanite_todo_*` — task tracking within the plan
- `nanite_show_*` — plan output rendering
- `nanite_message_*`, `nanite_handoff_*` — receive task from Chat, return plan
- `nanite_scratchpad_*` — working memory
- `fetch_tool_result`, `search_tool_result` — meta-tools
- Read-only context access: `doc-search`, memory read (`mcp__conduit__memory_recall`)

**Explicitly does NOT need:**
- `nanite_execute_task` — Planner produces a plan; it does not dispatch Workers itself (dispatch is Chat's job)
- `dev_write`, `dev_edit`, `shell_exec` — no execution
- `mcp__engine__task_create` at plan time — execution-time tool; out of scope

### Prompt anchor

```
You are a Planner agent. You receive an open-scope task and produce a structured, sequenced plan.
Your output is a plan — named phases, concrete steps, estimates, dependencies — not code or execution.
Use nanite_plan_* to record the plan. Use nanite_todo_* for discrete items within each phase.
Surface ambiguities as blocking questions, not assumptions. Stop when the plan is reviewable.
Do not execute steps yourself. Do not dispatch sub-agents. Return the plan to the dispatcher.
If the scope is narrower than expected, say so — the Chat agent may downgrade to a Worker.
```

### ScopeTier hint mapping

Planner is the target of `AssignRole` when `tier == TierOpen && pattern == PatternSubagent`. This is the only direct Planner routing in the current dispatch map. M3 may extend routing to `TierLarge × PatternSubagent` as the Planner identity matures in Phase 6. The Planner slug (`planner`) is reserved by migration 028 for M3 reflex dispatch without surface-area changes.

| ScopeTier | ExecutionPattern | Routes to |
|-----------|-----------------|-----------|
| `open` | `subagent` | **Planner** |
| `large` | `subagent` | Worker (current) — candidate for Planner in Phase 6 |
| `*` | `background` | Worker (always) |
| `trivial`–`medium` | any | Worker |

### Lifecycle

**Lifecycle:** Subagent (dispatched by Chat via `AssignRole`); single-shot per planning turn.
**Ephemeral or persistent:** Ephemeral — produces a plan artifact (persisted via `nanite_plan_*`), then exits.

### Composition

Planner is dispatched by Chat. Planner does not dispatch Workers — it returns a plan, and Chat (or the user) decides what to execute and when. Can receive Strategist analysis as context. Planner output (a `nanite_plan_*` artifact) is the input to subsequent Worker dispatches.

### Anti-patterns

- **Planner executing its own plan** — once a Planner runs tools to implement steps, it has become a Worker. Keep decomposition and execution in separate agents.
- **Skipping Planner for `TierOpen` tasks** — dispatching a Worker on an open-scope task without a plan produces unstructured execution and scope drift.
- **Planner dispatching Workers** — dispatch is Chat's responsibility. Planner should return the plan, not act on it.
- **Using Planner for small tasks** — `TierTrivial` through `TierMedium` tasks don't warrant decomposition overhead; route to Worker directly.

---

## 4. Researcher — Investigate and Summarize

**Name:** Researcher
**One-line purpose:** Gather, read, and synthesize information from internal or external sources; return a digest.
**When this is the right fit:** When the task is information retrieval and synthesis — "what does X say about Y", "find all references to Z", "summarize the state of Q." Single-shot or multi-step fetch-and-summarize. Does not produce executable plans or write code.

### Tool surface

**Needs:**
- `doc-search`, `mcp__conduit__context_search` — internal KB search
- `fetch_tool_result`, `search_tool_result` — meta-tools
- `web_fetch` (if configured) — external content retrieval
- `dev_read`, `dev_glob`, `dev_grep` — local file content reading (read-only)
- `nanite_show_document`, `nanite_show_report` — structured digest output
- `nanite_message_*` — receive task, return findings
- `nanite_scratchpad_*` — working memory across multi-step fetch

**Explicitly does NOT need:**
- `dev_write`, `dev_edit`, `shell_exec` — no mutation
- `nanite_execute_task` — does not dispatch further agents
- `nanite_plan_*`, `nanite_todo_*` — research findings are not plans

### Prompt anchor

```
You are a Researcher. Your job is to find, read, and synthesize information — not to act on it.
Retrieve data from the sources you have access to; read primary sources, not cached summaries.
Synthesize findings into a clear digest: what you found, where, and how confident you are.
Flag gaps and contradictions explicitly. Do not fill gaps with inference or guess.
Return the digest to the dispatcher; do not initiate follow-up actions.
If the question requires an action to answer (e.g., running a build), say so and stop.
```

### ScopeTier hint mapping

Researcher is a Worker specialization. In the current dispatch map it lands as `RoleWorker` across all ScopeTier × PatternSubagent combinations (except `TierOpen` which goes to Planner). M3 routes to a Researcher-profile Worker when intent signals indicate an investigation-only task. Typical ScopeTier range: `small` through `large`; a `trivial` question can be answered inline by Chat; `open`-scope research should first go through Planner for decomposition.

### Lifecycle

**Lifecycle:** Subagent (dispatched by Chat); single-shot or multi-step fetch.
**Ephemeral or persistent:** Ephemeral — task-scoped. Multi-step fetch occurs within one agent session, not across multiple dispatches.

### Composition

Dispatched by Chat or as a sub-step in a Planner-produced plan. Returns a structured digest. Does not dispatch further agents. Researcher output commonly feeds Strategist (for decision framing), Documentor (for capture), or Planner (as research context before decomposition).

### Anti-patterns

- **Giving Researcher write tools** — the boundary between research and documentation must be explicit; once a Researcher can write, it conflates two distinct responsibilities.
- **Using Researcher for active debugging** — debugging requires execution (running tests, compiling). That is Worker territory. Researcher reads; it does not run.
- **Leaving Researcher session-persistent** — accumulated context across unrelated research tasks degrades focus. Dispatch a fresh Researcher per task.
- **Routing open-scope research to Researcher directly** — research with unbounded scope needs decomposition first (Planner), then targeted Researcher dispatches per phase.

---

## 5. Documentor — Capture and Structure

**Name:** Documentor
**One-line purpose:** Receive existing information and produce well-structured documentation; low creative latitude, high fidelity.
**When this is the right fit:** When the work is "write this down correctly" rather than "figure out what to write." Content is provided (by Researcher, Strategist, or Worker); Documentor's job is structure and clarity. ADRs, API references, changelogs, context docs, KB entries.

### Tool surface

**Needs:**
- `dev_write`, `dev_edit` — file writing (documentation output target)
- `dev_read`, `dev_glob` — read existing docs to avoid duplication and match style
- `doc-note`, `doc-search` — KB write and search (Vanta Conduit integration)
- `mcp__conduit__knowledge_write`, `mcp__conduit__memory_write` — Vanta capture
- `nanite_show_document` — rendered document output for preview
- `nanite_message_*` — receive content payload, return doc artifact
- `nanite_scratchpad_*` — working memory

**Explicitly does NOT need:**
- `shell_exec`, `dev_exec` — no execution
- `nanite_execute_task` — does not dispatch further agents
- `nanite_plan_*` — not a planner
- `web_fetch` — content is provided; Documentor does not research

### Prompt anchor

```
You are a Documentor. You receive information and produce accurate, well-structured documentation.
Do not invent content. Your fidelity to the source material is your primary constraint.
Use the style and format conventions of the target document type (ADR, API ref, KB entry, context doc).
Write once; do not iterate speculatively. If source material is incomplete, say so — do not fill gaps.
Return the document artifact; do not initiate follow-up actions or make decisions about content.
```

### ScopeTier hint mapping

Documentor is a Worker specialization. Routes as `RoleWorker` across `TierSmall` through `TierLarge` when M3 detects a documentation-only intent signal (write + structured + no execution). `TierTrivial` documentation is handled inline by Chat or Worker without a dedicated Documentor dispatch. `TierOpen` documentation tasks (e.g., "document the entire API surface") should be decomposed by Planner first.

### Lifecycle

**Lifecycle:** Subagent (dispatched by Chat); single-shot per document.
**Ephemeral or persistent:** Ephemeral — one document task in, artifact out.

### Composition

Commonly the final step in a multi-agent pipeline: Researcher gathers → Strategist frames → Documentor captures. Can also operate standalone when content is handed directly by the user or Chat. Does not dispatch further agents. Output is a file artifact or Vanta KB entry.

### Anti-patterns

- **Giving Documentor execution tools** — if it can run tests or compile, it has drifted into Worker territory.
- **Asking Documentor to research its own content** — research is Researcher's job. Documentor receives content, not discovers it.
- **Allowing creative latitude** — Documentor should not invent content or fill gaps with inference. When source material is thin, it should stop and report the gap, not paper over it.
- **Using Documentor for Vanta memory hygiene** — memory hygiene (deprecation, deduplication, consolidation) belongs to a Curator role (not in this catalog yet; pending Mux directory). Documentor writes new artifacts; it does not manage existing memory.

---

## 6. Worker — Execute Defined Tasks

**Name:** Worker
**One-line purpose:** Execute a well-scoped task with full tool access; the harness's general-purpose execution agent.
**When this is the right fit:** When the task is concrete, bounded, and ready for execution — "implement X", "fix bug Y", "run migration Z", "refactor function Q." The default dispatch target for most tasks.

### Tool surface

**Needs:** Full task-appropriate surface — governed by the spawned agent profile's own `tool_permissions`, not this package. The harness spec allows Worker to have any tools its profile grants. Typical surface:
- `dev_read`, `dev_write`, `dev_edit`, `dev_glob`, `dev_grep`
- `shell_exec` / execution tools
- `mcp__*` (MCP server access per profile)
- `nanite_message_*`, `nanite_show_*` — result return
- `fetch_tool_result`, `search_tool_result` — meta-tools

**Does NOT need (by convention, not hard enforcement):**
- `nanite_execute_task` — Worker does not dispatch sub-workers (Chat dispatches)
- `nanite_plan_*` — planning is Planner's job
- The raw spawn primitive (`nanite_spawn_subagent`) — dispatch goes through Chat

The built-in `worker` profile (`config/agents/worker.yaml`) uses `allow: ["*"]` — any tool the session has loaded. Specialization profiles (e.g., `nanite-backend`) may narrow this.

### Prompt anchor

```
You are a Worker agent in the Nanite harness. You are dispatched to execute a specific task.
Complete the assigned work, then return a result. Do not expand scope beyond what was assigned.
Follow best practices. Explain your reasoning when output is not self-evident.
Do not initiate new conversations, ask clarifying questions mid-task, or spawn sub-agents.
If a blocker arises, report it in your result rather than guessing or working around it.
Return when done; the dispatcher decides next steps.
```

### ScopeTier hint mapping

Worker is the default dispatch target. `AssignRole` routes to Worker for all patterns except `TierOpen × PatternSubagent` (Planner) and all `PatternBackground` routes (Worker with `ModeAsync`).

| ScopeTier | ExecutionPattern | Routes to |
|-----------|-----------------|-----------|
| `trivial` | `inline` | Worker (sync) |
| `small` | `inline` | Worker (sync) |
| `small`–`large` | `subagent` | Worker (sync) |
| `*` | `background` | Worker (async) |
| `open` | `subagent` | Planner |

### Lifecycle

**Lifecycle:** Subagent (dispatched by Chat via `AssignRole`) or background task (async).
**Ephemeral or persistent:** Ephemeral — task-scoped. Background variant runs to completion without caller blocking.

### Composition

Dispatched by Chat. Does not dispatch further agents. Can receive a plan from Planner as its input (via the task context passed through `nanite_execute_task`). Returns a result envelope to Chat. Worker specializations (backend, frontend, reviewer, docs) share the pattern but narrow the tool surface and system prompt.

### Anti-patterns

- **Dispatching Worker for open-scope tasks without a plan** — open-scope tasks need Planner first. Handing an undecomposed open task directly to Worker produces scope drift.
- **Giving Worker the dispatch primitive** — if Worker can call `nanite_execute_task`, the dispatch chain has no clear root. Chat owns dispatch.
- **Session-persistent Worker** — Workers are task-scoped. Accumulating state across unrelated tasks produces context pollution.
- **Using a single Worker profile for all specializations** — while `allow: ["*"]` is valid for the base Worker, specialization profiles should narrow the surface to what the domain actually needs.

---

## 7. Reviewer — Independent Assessment

**Name:** Reviewer
**One-line purpose:** Independently assess a body of work — code, plan, document, or design — and return findings with severity; no edit capability.
**When this is the right fit:** When the task is "evaluate this" rather than "fix this." PR reviews, plan reviews, design audits, security scans, output verification. Independence is the core value: a Reviewer that can also edit cannot give an unbiased assessment.

### Tool surface

**Needs:**
- `dev_read`, `dev_glob`, `dev_grep` — read the artifact under review
- `shell_exec` (limited) — run tests, linters, build checks (read-only outputs)
- `nanite_show_report` — structured findings output
- `nanite_message_*` — receive artifact reference, return findings
- `nanite_scratchpad_*` — working memory
- `fetch_tool_result`, `search_tool_result` — meta-tools
- `doc-search` — lookup conventions and standards for comparison

**Explicitly does NOT need:**
- `dev_write`, `dev_edit` — no edits; findings only
- `nanite_execute_task` — does not dispatch further agents
- `nanite_plan_*`, `nanite_todo_*` — not a planner
- `mcp__engine__task_create` — does not create tasks (that is the caller's job after reading findings)

### Prompt anchor

```
You are a Reviewer. Your job is to assess the artifact you are given — not to fix it.
Read the code, plan, or document carefully. Identify issues, risks, and gaps.
Report findings using BLOCK / WARN / NOTE severity: BLOCK means do not proceed, WARN needs attention, NOTE is advisory.
Do not speculate about intent. Cite the specific location (file:line, section, field) for each finding.
Do not propose rewrites. You may suggest the direction of a fix, but not implement it.
Return your findings report when the review is complete.
```

### ScopeTier hint mapping

Reviewer is a Worker specialization (read-only Worker). Routes as `RoleWorker` when M3 detects a review-only intent signal. Typical ScopeTier range: `small` through `large`. A `trivial` review (e.g., "does this look right?") can be handled inline by Chat. An `open`-scope audit (e.g., "review the entire codebase architecture") should be decomposed by Planner into bounded Reviewer dispatches.

### Lifecycle

**Lifecycle:** Subagent (dispatched by Chat); single-shot per review artifact.
**Ephemeral or persistent:** Ephemeral — one artifact in, findings out.

### Composition

Dispatched by Chat. Can be composed after Worker (review the output), after Planner (review the plan), or independently (PR review). Reviewer findings commonly feed a Worker (fix the issues), Strategist (interpret tradeoffs in the findings), or Documentor (capture findings as an audit report). Does not dispatch further agents.

### Anti-patterns

- **Giving Reviewer write tools** — the moment a Reviewer can edit, it loses independence and becomes a Worker. Keep surfaces separate.
- **Using Reviewer for iterative refinement** — if the task is "fix this until it passes review," that is a Worker loop with a Reviewer gate — two separate agents, not one Reviewer iterating on its own output.
- **Routing open-scope audits directly to Reviewer** — decompose first (Planner), then dispatch bounded Reviewer tasks per phase.
- **Reviewer proposing implementations** — findings are the product. Proposed rewrites belong in a follow-up Worker dispatch, not in the Reviewer's output.

---

## Cross-Reference Table

| Pattern | Tool Surface (key prefixes) | ScopeTier hint | ExecutionPattern hint | Lifecycle |
|---------|----------------------------|----------------|----------------------|-----------|
| **Chat** | `nanite_todo_*`, `nanite_plan_*`, `nanite_scratchpad_*`, `nanite_message_*`, `nanite_handoff_*`, `nanite_show_*`, `nanite_execute_task` | N/A (session entry; detected via prompt template slug `chat-role-harness`) | N/A (not dispatched) | Session-bound; static |
| **Strategist** | read-only knowledge, `nanite_show_*`, `nanite_message_*`, `nanite_scratchpad_*` | `trivial`–`medium` | `inline` / `subagent` | Subagent; ephemeral |
| **Planner** | `nanite_plan_*`, `nanite_todo_*`, `nanite_show_*`, `nanite_message_*`, read-only knowledge | `open` (primary); `large` (candidate Phase 6) | `subagent` | Subagent; ephemeral |
| **Researcher** | `dev_read`, `dev_glob`, `dev_grep`, `doc-search`, `web_fetch` (opt), `nanite_show_*`, `nanite_message_*` | `small`–`large` | `subagent` | Subagent; ephemeral |
| **Documentor** | `dev_write`, `dev_edit`, `dev_read`, `doc-note`, `mcp__conduit__knowledge_write`, `nanite_show_document` | `small`–`large` | `subagent` | Subagent; ephemeral |
| **Worker** | `allow: ["*"]` (profile-governed; full execution surface) | `trivial`–`large` (all); `*` × `background` | `inline` / `subagent` / `background` | Subagent; ephemeral; async for background |
| **Reviewer** | `dev_read`, `dev_glob`, `dev_grep`, `shell_exec` (read-only outputs), `nanite_show_report`, `nanite_message_*` | `small`–`large` | `subagent` | Subagent; ephemeral |

### AssignRole dispatch map (from `internal/dispatch/role.go`)

```
classify.PatternBackground            → RoleWorker  (ModeAsync,  slug: worker)
classify.TierOpen + PatternSubagent   → RolePlanner  (ModeSync,   slug: planner)
everything else                       → RoleWorker  (ModeSync,   slug: worker)
```

Strategist, Researcher, Documentor, and Reviewer are Worker-pattern specializations. M3 routes to specialized profiles by matching intent signals against registered agent profiles **after** `AssignRole` selects `RoleWorker`. The pattern catalog defines the behavioral contract; M3 implements the profile-selection logic.

---

*Note:* The Planner slug (`planner`) is reserved by migration 028 (CW-20260426-0016). Dispatch can target `PlannerRoleSlug` today; the full Planner identity lands in Phase 6. Researcher, Reviewer, and Worker route as `RoleWorker` with profile selection in the DB-backed `dispatch_to_agent` reflex layer (`internal/agent/reflexes/seeds.go`) — no `AssignRole` changes needed. Strategist and Documentor have no matching agent profile and were deliberately not migrated into that reflex layer (`TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`) — routing to them is not reachable until a real profile exists for either.
