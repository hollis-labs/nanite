# Nanite Architecture Snapshot

**Date:** 2026-04-05  
**Scope:** Precise mapping of Nanite's architecture against Anthropic's published agent/harness design patterns  
**Audience:** Downstream synthesis and architecture comparison  

---

## 1. Chat Engine Loop

### Overview

The core chat loop lives in `internal/chat/engine.go` (1760 lines). The main entry point is `generateResponse(ctx, sessionID, assistantMsgID, userContent, ch)` invoked asynchronously from `HandleMessage()` (`engine.go:256-278`).

### Turn-by-Turn Execution

**Context Assembly** (`engine.go:514`)
- Calls `e.Broker.AssembleContext()` which loads the system prompt, recent messages, and applies the context broker (if available)
- System prompt assembled from agent profile + mode + workspace via `assembleSystemPromptFromTemplates()` (`context.go:39-80`)
- Falls back to legacy assembly if no templates assigned
- Token budget: 75% of 200k tokens (150k) by default; adjustable via `context_client.BudgetPct` (`context_client.go:35-36`)

**Tool Selection** (`engine.go:582`)
- Calls `e.getToolsForAgent()` which applies progressive discovery if tool count > 5 (`engine.go:53`)
- Progressive discovery injects a tool catalog into the system prompt and enables the `request_tools` meta-tool
- The `request_tools` meta-tool (`toolclient/meta_tools.go`) allows the LLM to request full schemas for specific tools by name or intent

**Provider Call** (`engine.go:695-758`)
- Streams via `prov.StreamChatWithTools()` if tools available, else `prov.StreamChat()`
- Provider adapter handles platform-specific streaming (Anthropic content blocks, OpenAI tool_calls, PTY CLI line parsing)
- Response accumulated in `turnContent` (text) + `toolUseBlocks` (structured tool calls)
- Handles PTY (CLI) provider specially: sandbox dir setup (`engine.go:706-750`), CLI session resume, process tracking

**Tool Execution Loop** (`engine.go:952-1272`)
- Iterates over `toolUseBlocks` from the LLM's response
- Special case: `request_tools` meta-tool is intercepted and handled by `e.ToolClient.HandleRequestTools()` (`engine.go:956-1026`)
  - Loads new tools if found, updates the `loadedTools` map, appends new schemas to `tools` slice
  - Tracks consecutive empty results (hard cap: 3 total calls or 2 consecutive empties)
- Regular tool execution:
  - Pre-check: skip if tool is blocked (identical results 3x) (`engine.go:1044-1062`)
  - Execute via `e.ToolClient.CallTool()` or `e.MCPManager.ExecuteTool()` (`engine.go:1067-1160`)
  - Emit tool_call and tool_result SSE events for UI
  - Detect stuck loops: same tool with identical result → escalate warning, then block (`engine.go:1190-1213`)
  - Truncate large outputs via `truncate.Output()` (preserves full output to disk) (`engine.go:1219`)
  - Capture envelope data from tool results (markers: `<!--ENVELOPE_DATA:...:ENVELOPE_DATA-->`) (`engine.go:1098-1110`)
- Append tool results as a user message for the next turn (`engine.go:1274-1278`)
- Enforce token budget before each iteration (`engine.go:678-693`)

**Loop Termination** (`engine.go:908-925`)
- Stop if `stopReason != "tool_use"` (i.e., `max_tokens`, `end_turn`, or other terminal reason)
- Stop if tool list is empty (no more calls)
- Enforce iteration cap: default 10 iterations, configurable via `AgentConstraints.MaxIterations` (`engine.go:655-658`)
- Hard ceiling: 100 iterations if no agent constraint set (`engine.go:62-64`)
- Check retry budget and circuit breaker before each next iteration (`engine.go:1285-1303`)

### Tool Error Handling

**Errors are non-blocking** (`engine.go:1164-1188`):
- Tool failure increments `consecutiveToolErrors`
- After 3 consecutive errors, escalates warning level from "warning" to "critical"
- Retry budget (if set) is decremented on each error
- Tool result includes error message, fed back to LLM for adaptive recovery

**Blocked Tools** (`engine.go:1044-1062`, `1194-1202`):
- When same tool returns identical result 3x in a row, it is hard-blocked
- Future calls to that tool are skipped with a "BLOCKED" message
- Forces the LLM to try a different approach or explicitly fail the task

**Truncation** (`engine.go:1216-1253`):
- Large tool outputs (>1000 chars typically) are truncated with a summary suffix
- Full output saved to disk (logged in `truncate/`)
- If orchestrator is configured, hints delegation instead of truncation
- Truncation logged as event for audit trail

### Streaming & Presence

**SSE Events** (`engine.go:129-144`):
- `stream_start`: agent ID, message ID
- `delta`: accumulated text content
- `tool_call`: tool name, tool ID
- `tool_result`: tool name, tool ID, summary (500 chars max)
- `tool_warning`: JSON payload with tool name, error, iteration, consecutive error count
- `status`: status messages (truncation notice, rate limit status)
- `circuit_open`: circuit breaker tripped
- `stream_end`: final usage counts, envelope JSON
- `error`: error event with structured error code

**Session-Level SSE Deduplication** (`engine.go:302-336`):
- One active SSE connection per session
- New connections evict the old one via `session_takeover` event
- Prevents ghost streams when browser reconnects

**Presence Broadcast** (`engine.go:373-392`, `356-368`):
- `broadcastPresence()` sends events to all connected presence clients
- Throttled `cli_active` for PTY sessions (configurable throttle, default 5s)
- `tool_pending`/`tool_resolved` events for each tool execution (PTY manages tools internally)
- Slow clients have events dropped rather than blocking the sender

### Streaming Multiplexing

**Per-Turn Buffering**:
- SSE events written to a buffered channel (128 items) immediately as they occur
- Client consumes asynchronously via `/api/stream/{messageID}`
- If client is slow, events are dropped (presence throttle) or channel fills (SSE waits briefly)
- No adaptive flow control; producer-driven throughput

**Concurrency Model**:
- Each message has its own goroutine (`generateResponse()`) spawned from `HandleMessage()`
- Goroutines can run in parallel for different sessions
- Within a session, only one message generation runs at a time (implicit via single stream per session)
- Tool execution is serialized (no parallelism in the current loop)

---

## 2. Context Assembly & Management

### System Prompt

**Assembly Path** (`context.go:39-80`, `context_client.go:48-116`):

1. Load templates for agent via `s.ComposePromptForAgent()` if configured
2. Fall back to legacy assembly: agent.SystemPrompt + mode.PromptAddendum + workspace description
3. Optionally enrich with universal context broker (`context_client.go:66-68`)
4. Inject native tool usage guide (absolute paths, dev_glob/dev_grep parameter separation) (`engine.go:88-99`)
5. If progressive discovery active, inject tool catalog (`engine.go:610-612`)

**Token Accounting**: System prompt tokens estimated via `EstimateTokens()` (chars/4) and tracked in `TokenBreakdown` struct (`context_client.go:165-172`)

### Message History

**Fetching** (`context_client.go:70-74`):
- Load up to 200 messages from session history via `e.Store.ListMessages(session.ID, 200)`
- All roles converted to "user" or "assistant" for provider API (tool/system role not sent to LLM)

**Budget Enforcement** (`context_client.go:86-103`):
- Estimate total tokens: system + messages + tools
- If over budget (default 75% of 200k = 150k), drop oldest messages until under budget
- Hard ceiling: 80% of context window (160k tokens); if total still over, return error

**Pruning & Compaction** (`context_client.go:127-163`):
- `PruneAfterTurn()` runs after each turn
- Tool-role messages older than 2 turns from the end with content > 500 chars are marked `IsCompacted` and replaced with compact summary
- E.g., `[compacted: tool output, 12345 chars]`
- Reduces context bloat for long-running sessions

### Context Broker Integration

**Sources** (`contextbroker/broker.go`):
- **CortexSource**: queries Cortex via MCP tools (`source_cortex.go:54-98`)
  - Primary: `mcp__vanta__context_broker_fetch` (intent-based retrieval)
  - Fallback: `mcp__vanta__context_search` (keyword search; currently broken without embedding provider)
  - Intent classification: user message → 7 intent types (write_code, debug_issue, plan_feature, recall_decision, etc.) → mapped to Cortex's 4 native types
  - Budget: 30-40% of context window (default 15-20k tokens)
- **PCCSource**: reads local `.agentrc/pcc/` files
- **SessionSource**: recent chat history (max 50 messages)
- **EngineSource**: tasks/sprints from Fragments Engine
- **HadronBlueprintGate**: session-start blueprint cache

**Enrichment** (`context_client.go:303-348`):
- Called during system prompt assembly before every chat turn
- Fetches relevant context from all sources based on session/agent/workspace
- Injected into system prompt via structured markers

### Token Budget Model

**Per-Turn Enforcement** (`context_client.go:223-288`):
- `EnforceTokenBudget()` runs before every provider call
- Ceiling: 80% of 200k = 160k tokens (hard limit)
- Breakdown tracked: system (est), messages (est), tools (est)
- Reduction cascade if over budget:
  1. Prune old tool results (replace with compact markers)
  2. Reduce tool count (drop from end)
  3. Drop oldest messages
  4. If still over: error (refuse to send)

**Tool Token Budgeting** (`toolclient/broker.go:88-99`):
- `EstimateToolDefTokens()` sums all tool schemas (JSON marshaled, chars/4)
- Tools pruned to fit token budget (default 25% of context window = 50k tokens)
- Token budget is percentage-configurable via `ToolTokenBudgetPct`

### Retrieval

No per-turn retrieval beyond message history + context broker sources. The context broker is the only mechanism for external knowledge injection (via Cortex or PCC files).

---

## 3. Tool System

### Registration & Discovery

**Tool Sources** (`toolclient/broker.go:24-54`):
1. **Built-in tools**: always available (registered in `mcp/dev_tools.go`, `mcp/general_tools.go`, `mcp/self_tools.go`)
2. **MCP-managed tools**: discovered from registered MCP servers
3. **Tool broker**: intent-based selection via `LocalBroker.SelectTools()` (from `tool-broker` library)

**Tool Definition Format**:
- Name (string)
- Description (string)
- InputSchema (JSON Schema)
- Server (MCP server name prefix, e.g., "engine", "cortex")

**Naming Convention**:
- Built-in: unprefixed (e.g., `dev_read`, `web_fetch`)
- MCP tools: prefixed `mcp__{server}__{tool_name}` (e.g., `mcp__engine__engine_sprint_create`)

### Progressive Disclosure

**Threshold** (`engine.go:53`):
- If tool count > 5, activate progressive discovery
- All tools sent as summaries (name + description, truncated to 80 chars)
- Tool catalog injected into system prompt with note: "use request_tools to get full details"

**Request Meta-Tool** (`toolclient/meta_tools.go`, `engine.go:956-1026`):
- LLM can call `request_tools` with:
  - `tool_names`: explicit array of tool names to fetch
  - `intent`: keyword/phrase to match against tool descriptions
- `HandleRequestTools()` filters broker catalog by intent or names, returns up to 5 new tools with full schemas
- Hard cap: 3 total `request_tools` calls or 2 consecutive empty results
- Once limit hit, hint: "proceed with tools you have, do NOT call request_tools again"

### Selection & Permissions

**Selection Flow** (`toolclient/broker.go:60-108`):
1. Intent-based via `LocalBroker.SelectTools()` (external tool-broker library)
2. Cap at `MaxSelectedTools` (15)
3. Apply token budget pruning
4. Log selection with intent, tool names, token budget

**Permission Model** (`toolclient/permissions.go`):
- Agent-level tool allowlist: `agent.ToolPermissions` (JSON)
- `CheckPermission(agentID, toolName)` returns true/false
- Default: all tools allowed if no denylist set
- If denylist present: tool must not match any blocked pattern

**Filtering by Agent** (`toolclient/broker.go:129-144`):
- After broker selection, tools filtered through `tb.CheckPermission(agentID, toolName)`
- Tools without permission are silently dropped (no error to LLM)

### Result Truncation

**Strategy** (`truncate/`):
- `Output(result, toolName, opts)` returns `{Truncated, Content, OriginalLen, OutputPath}`
- Default max: 1000 chars for text, shorter for binary
- If truncated, full output saved to disk (session-scoped directory)
- Tool name used to infer content type (e.g., dev_write → file content, web_fetch → HTML body)
- Optional delegation hint (if orchestrator configured): "Consider delegating this to a sub-agent"

**Result in Context**:
- Truncated content sent to LLM
- Full content available via OutputPath for later inspection

---

## 4. Multi-Agent Orchestration

### Decomposition & Planning

**Orchestrator Struct** (`orchestrator.go:40-54`):
- Holds reference to Provider registry, MCP manager, and Decomposer
- `HasDecomposer()` checks if decomposer is available

**Decomposition** (`orchestrator.go:186-251`):
- `DelegateAndAggregate()` is the main entry point
- Calls `e.Orchestrator.Decomposer.DecomposeTask()` to break down user message into sub-tasks
- Decomposer is initialized with provider registry for LLM calls
- Returns `DecompositionResult`: `SubTasks` array, `Aggregation` strategy, `IsComplex` flag

**Planning** (`orchestrator.go:62-96`):
- `BuildPlan()` creates orchestration plan from decomposition
- Checks for Engine and Cortex availability via `hasToolPrefix()` (checks tool names in MCPManager registry)
- Creates Engine sprint + tasks if Engine available (via MCP tool calls) (`orchestrator.go:168-211`)
- Returns `OrchestrationPlan` with sub-tasks, aggregation strategy, sprint/task IDs, availability flags

### Sub-Task Execution (Delegation)

**Worker Session Creation** (`delegate.go:40-184`):
- `DelegateTask(ctx, req)` spawns a worker session for each sub-task
- Worker session inherits: agent (defaults to parent's agent), mode, model, workspace
- Task content formatted as markdown: `## Delegated Task: {title}\n\n{description}`
- Worker session created with metadata: `{"delegation":true,"parent_session_id":"...","task_title":"..."}`
- Worker responds via `generateResponse()` (recursive call with same engine)

**Aggregation** (`orchestrator.go:98-145`):
- `Aggregate()` collects all sub-task results
- Builds prompt: "Aggregation strategy: {strategy}\n\nCombine results below..."
- Calls provider (usually same as main agent) to synthesize final response
- Fallback: if no provider, concatenates results
- Returns `OrchestrationResult` with plan, per-task results, final output

### Worktree Model

**Not Yet Implemented** in the chat engine. Delegation spawns full worker sessions with their own message history, not worktrees. The `internal/chat/delegate.go` file shows the pattern but actual worktree isolation (file system, process, state) is listed as deferred in `vnext-mvp.md:285`.

### Handling Sub-Agent Results

**Result Aggregation**:
- Each sub-task completes with `{Title, Output, Error, TaskID}`
- If sub-task failed (error set), still included in aggregation but marked with error
- Aggregation LLM sees all results (successes + failures) and synthesizes holistic response

---

## 5. Plugin Host, Events, Hooks, Filters

### Event Catalog

**Defined Events** (`plugin/events.go:15-64`):

| Category | Events | Emitted? |
|----------|--------|----------|
| Session | `session.start`, `session.end`, `session.archived` | Partial (start yes, end/archived no) |
| Agent | `agent.switched`, `agent.loaded` | No |
| Message | `message.sent`, `message.received`, `message.deleted` | Partial (received yes, sent/deleted no) |
| Mode | `mode.changed`, `scope.changed` | Partial (changed yes, scope no) |
| Tool | `tool.called`, `tool.failed`, `tool.complete` | Yes (called/failed), (complete no) |
| UI | `envelope.rendered`, `widget.loaded`, `action.triggered` | No |
| Workflow | `workflow.started`, `workflow.complete`, `workflow.failed` | No |
| Config | `config.changed` | No |
| Plugin | `plugin.installed`, `plugin.uninstalled` | No |
| Provider | `provider.error`, `provider.fallback` | Partial (error yes, fallback no) |

**Total Dead Events**: 21 (per `plugin-hooks-events-filters.md:19-20`)

**Alias Map** (`plugin/events.go:71-78`):
- Claude Code hook names map to Nanite names: `PreToolUse` → `tool.executing`, `PostToolUse` → `tool.complete`, etc.
- Normalized on registration; always emitted under Nanite name

### Pre-Hooks (Cancellation)

**Defined but Not Wired**:
- `EventMessageSending`: before LLM call, can cancel with "cancel" key in event data
- `EventToolExecuting`: before tool execution, can cancel to skip tool

**Use Cases**:
- Content policy plugin blocks certain prompts
- Rate-limit plugin pauses execution
- Permission plugin blocks dangerous tools

### Plugin Host Architecture

**Host Struct** (`plugin/host.go:53-80`):
- Maps plugins by ID, event hooks by event type
- Tracks UI components, connectors, keybindings, slots
- Manages store (DB-backed settings), router, event subscriptions

**Event Emission** (`plugin/host.go`):
- Plugins register hooks via `RegisterEventHook(eventType, hook)`
- Host calls all hooks for an event in registration order
- Pre-hooks can signal cancellation; post-hooks are fire-and-forget
- Event data: structured `EventData` struct converted to map

**Service Registration**:
- Plugins can register: connectors, providers, CLI adapters, UI components, keybindings, commands, hooks
- Services accessible to plugins via `host.GetService(name)`

### Filter System (Proposed)

**Design** (`plugin-hooks-events-filters.md:110-150`):
- Not yet implemented
- Synchronous pipeline: each handler receives output of previous, ordered by priority
- Filter points proposed:
  - `system_prompt`: after assembly, before LLM call
  - `user_message`: after receive, before LLM sees it
  - `tool_result`: after execution, before context entry
  - `assistant_response`: after LLM, before persist/render
  - `context_window`: full assembled messages before LLM call
  - `envelope_data`: envelope payload before frontend render
  - `shell_command`: before execution (upcoming shell feature)
  - `shell_output`: before it enters conversation

### UI Slots

**Defined Slots** (`plugin/host.go:68`):
- `slots` map: slot name → registered entries (priority ordered)
- Slots are opaque strings; no enforced enum

**Unrendered Backend Slots**:
- `context-menu:message`
- `context-menu:session`
- `command-palette`

**Pending Frontend Slots** (not yet wired):
- `message-actions`: per-message footer buttons
- `message-header`: per-message badges
- `composer-above`: above input (needed for shell feature)
- `composer-below`: below input

---

## 6. Workflow Engine

**Status**: Defined but not integrated into chat loop.

**Components**:
- `internal/workflow/` package exists
- Loader and engine classes defined
- YAML schema for workflow definitions

**Integration**: Workflows are referenced in self-service tools (`mcp/self_tools.go`) but not triggered during normal chat. They can be executed explicitly via API but are not part of the main chat turn loop.

**Future**: Phase 7 of `vnext-mvp.md` skips workflow integration, deferring it to post-MVP work.

---

## 7. Memory & Continuity (Phase C Investigation)

**Current State** (`phase-c-cortex-investigation.md`):

### Cortex Integration Today

- Nanite queries Cortex via MCP tools (`context_broker/source_cortex.go`)
- Two-stage fallback:
  1. Primary: `mcp__vanta__context_broker_fetch` (intent-based retrieval)
  2. Fallback: `mcp__vanta__context_search` (keyword; currently broken without embedding provider)
- Intent mapping: 7 Nanite intents (write_code, debug_issue, etc.) → 4 Cortex types (lossy)
- Token budget: Cortex gets 30-40% of 50k budget (15-20k tokens)
- No explicit config; relies on MCP auto-discovery

### Registered Cortex Types

| Type ID | TTL | Notes |
|---------|-----|-------|
| `brief/summary` | 90d | Ephemeral |
| `decision/adr` | ∞ | Requires human approval |
| `system/map` | ∞ | Architecture maps |
| `task/spec` | ∞ | Task specifications |
| `strategy/goal`, `/constraints`, `/roadmap` | ∞ | Strategic planning |
| `config/service` | ∞ | Service configs |
| `contract/api`, `/data` | ∞ | Contracts |
| `runbook` | ∞ | Operational docs |
| `note/volatile` | 14d | Draft-only, short-lived |
| `project/identity` | ∞ | Project metadata |
| `principles` | ∞ | Highest rank bias (1.5) |
| `session/snapshot` | 30d | Session extractions |

**Registered Views**:
- `agent_boot`: startup context (system/map, principles, strategy, contracts)
- `briefing`: briefings (summaries, decisions, goals, maps)
- `strategy`: strategic planning (goals, constraints, roadmaps, decisions, maps)
- `task_exec`: task execution (specs, contracts, decisions, runbooks, maps)

**No memory-oriented view exists.**

### Gaps Identified

1. **No semantic search**: `context_search` returns "embedding_unavailable" (blocks primary retrieval)
2. **No memory type**: None of 15 types designed for persistent user/project memories
3. **No memory view**: No view filtering for memory content
4. **Namespace policies null**: No access control or retention policies

### Proposed Memory Type

**Recommended**: Single `memory` type with subtypes in metadata:
- Subtypes: `user`, `feedback`, `project`, `reference`
- Persistent (no TTL)
- Status progression: draft → reviewed → canonical → deprecated

**New View Needed**: `memory_recall` (returns memories ranked by status)

**Namespace Strategy**:
- `app/nanite/user/{user_id}` — cross-project user memories
- `app/nanite/project/{project_id}` — project-scoped memories
- `app/nanite/session/{session_id}` — session-staging (temporary extractions)

**Extraction Triggers**:
- PostCompact: rich extraction from compacted messages
- Per-Turn: lightweight extraction (corrections, preferences, decisions)
- Session End: promotion and deduplication

### Open Questions

- Embedding provider timeline for semantic retrieval?
- Dynamic namespace registration supported?
- Which model for extraction (session model vs utility model)?
- Memory cap before quality degrades?

---

## 8. Sandbox & Shell Plans

### Current Sandbox

**Sandbox Directory** (`sandbox/sandbox.go:19-30`):
- Per-session: `~/.nanite/sandboxes/{sessionID}/`
- Subdirectory: `~/.nanite/sandboxes/{sessionID}/.sandbox/` (reference files)
- Created on demand with mode 0755

**Sandbox Files** (`sandbox/sandbox.go:41-65`):
- `CLAUDE.md`: compact rules + pointers (survives CLI compaction)
- `.sandbox/envelope-schema.md`: full envelope spec
- `.sandbox/agent-context.md`: agent profile, mode, capabilities
- `.mcp.json`: Nanite MCP server config (CLI discovers tools)

**Use Case**: PTY-spawned CLIs (Claude, Codex, Gemini) receive sandbox dir + CLAUDE.md at startup. Re-reads CLAUDE.md after context compaction for persistent instruction updates.

### Shell Feature (Planned)

**3-State YOLO Model** (`shell/shell.go:9-42`, `shell/denylist.go:7-79`):

| Mode | Behavior | Default |
|------|----------|---------|
| `ask` | Explicit user confirmation for each command | Yes |
| `session` | Auto-approve within denylist constraints | No |
| `yolo` | Disable all restrictions | No |

**Denylist** (`shell/denylist.go:10-35`):
- 24 hardcoded destructive patterns (rm -rf /, mkfs, reboot, fork bomb, etc.)
- `Enabled()` toggle for YOLO mode
- `Check(command)` returns reason if blocked, else ""
- Case-insensitive prefix matching

**Command Parsing** (`shell/shell.go:30-43`):
- `ParseCommand(input)` extracts "!command" from message
- Returns ("", false) if not shell syntax

**Integration Points** (not yet wired):
- `internal/api/shell.go` — handlers for shell command execution
- `internal/shell/exec.go` — actual command execution
- Plugin filter points: `shell_command` (before exec), `shell_output` (before context)
- Plugin events: `shell.exec`, `shell.error`, `shell.blocked`
- UI slot: `composer-above` for shell info drawer

**Deferred**: Shell execution in the chat turn (currently defined but not integrated)

---

## 9. Open Threads Snapshot

### In-Flight Efforts

| Effort | Status | File | Est. Completion |
|--------|--------|------|-----------------|
| **Plugin hooks/events/filters wiring** | Task 1 (wire dead events) pending | `plugin-hooks-events-filters.md` | Phase post-MVP |
| **Shell feature** | Scaffold complete, integration deferred | `shell/*.go`, `internal/api/shell.go` | Phase 8+ |
| **Phase C memory system** | Investigation complete, design pending | `phase-c-cortex-investigation.md` | Phase 8 (post-MVP) |
| **Plugin extraction phases 5-6** | Not started | `plugin-extraction-plan.md` | Phase 8+ |
| **Workflow integration** | Deferred (engine defined, not triggered) | `internal/workflow/` | Phase 8+ |
| **Embedding provider** | External blocker (Cortex) | — | Unknown |

---

## 10. Known Anti-Patterns (Backend)

**Source**: `.agentrc/agents/backend.md` Anti-Patterns section

### Current Anti-Patterns

1. **God object: `Engine` struct** (1760 lines)  
   - File: `internal/chat/engine.go:130-148`
   - Consolidates chat engine, streaming, tool loop, context assembly, session mgmt, presence, utilities in one file
   - 12 fields + 6 `sync.Map` fields for concurrent state
   - Should split into focused subsystems

2. **Hardcoded MCP server paths**  
   - File: `cmd/nanite/main.go:377-419`
   - Absolute paths like `home + "/go/bin/engine"` are developer-machine-specific
   - Will break for other contributors
   - Should be config-driven

3. **Hardcoded Cortex MCP token**  
   - File: `cmd/nanite/main.go:407-409`
   - Hex token fallback is insecure and inflexible

4. **Hardcoded default model**  
   - Locations: `internal/api/messages.go:128`, `internal/chat/engine.go:156`
   - Should be a constant or config value

5. **Version string drift**  
   - File: `internal/server/server.go:88`
   - Returns hardcoded "0.2.0"; no central version constant

6. **Store.Seed() monolith** (428 lines)  
   - File: `internal/store/seed.go`
   - Seeds agents, templates, skills, modes, prompt templates inline with SQL
   - No structured seed data files

7. **Envelope sync fragility**  
   - Backend types: `chat/envelope.go`
   - Frontend registry: `ui/src/generated/plugin-envelopes.ts`
   - Manual sync required; missing backend type causes silent data loss
   - Test: `TestEnvelopeRegistrySync` validates sync

8. **Anonymous struct request bodies**  
   - Example: `internal/api/sessions.go:27-31`, repeated in all handlers
   - Every handler defines inline `var req struct {...}`
   - No shared request/response types; blocks API documentation and reuse

9. **Fat `main.go` wiring** (307 lines in `cmdServe()`)  
   - File: `cmd/nanite/main.go:62-369`
   - Manual dependency wiring; no DI container or wire framework
   - Every new subsystem requires editing main.go

10. **Inconsistent nil checks for optional deps**  
    - Some handlers return empty arrays, others return errors
    - No consistent pattern for optional dependency availability

### Resolved Anti-Patterns ✅

- **Broken connector imports** — Cleaned up during rebrand
- **mcp.TestSelfToolsTransport_ListTools** — Fixed via `>=` comparison
- **Seed/migration overlap** — Migrations squashed to DDL, seed consolidated
- **Manual migration ordering** — Fixed via `fs.ReadDir()` auto-discovery

---

## Summary Table: Architecture by Dimension

| Dimension | Implementation | File(s) | Status |
|-----------|----------------|---------|--------|
| Chat loop | 10-iter max, tool execution + budget enforcement | `engine.go` | Production |
| Streaming | Buffered SSE with multiplexing per session | `engine.go`, `api/messages.go` | Production |
| Context assembly | Broker-fed + templates, 75% budget, slot compaction | `context.go`, `context_client.go` | Production |
| Tool selection | Intent-based broker + progressive discovery | `toolclient/broker.go` | Production |
| Tool execution | Serial with truncation, stuck loop detection, blocking | `engine.go:952-1272` | Production |
| Multi-agent | Orchestrator + decomposition + delegation | `orchestrator.go`, `delegate.go` | Beta |
| Plugins | Host + events + pre-hooks, no filters yet | `plugin/host.go`, `plugin/events.go` | Beta (events incomplete) |
| Memory | Cortex integration via MCP, no semantic search | `contextbroker/source_cortex.go` | Alpha (gaps noted) |
| Shell | Denylist + 3-state YOLO, not integrated to chat | `shell/shell.go`, `shell/denylist.go` | Scaffold |
| Workflow | YAML engine defined, not triggered from chat | `internal/workflow/` | Incomplete |

---

**End of Snapshot**
