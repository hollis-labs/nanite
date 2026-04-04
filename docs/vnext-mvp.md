# Nanite vNext — MVP Selected Work

**Created:** 2026-04-02
**Status:** Planning
**Source:** [conduit-vnext-decisions.md](/Users/chrispian/Projects-apps/agent-workspaces/exploration/conduit-vnext-decisions.md)
**Service Layer Plan:** [piped-herding-stallman.md](/Users/chrispian/.claude/plans/piped-herding-stallman.md) (COMPLETE)

## Goal

Ship a beta-ready Nanite with clean architecture, solid chat experience, and no known bugs. This is the initial release — no existing users, no backward compatibility constraints. We are simplifying, fixing, refactoring, and polishing as we go. Each phase should leave the codebase better than it found it.

## Guiding Principles

- **Consult before architecture decisions.** Each phase starts with a quick alignment check before implementation begins.
- **Greenfield when optimal.** Since there are no users, prefer creating new modules alongside old ones and migrating when ready (the service layer pattern) over trying to reorganize in-place.
- **No functionality loss.** Everything that works today must still work after each phase.
- **Clean as you go.** Weave cleanup into each phase rather than batching it at the end.
- **Build passes at every checkpoint.** `go build`, `go vet`, `go test` clean after every phase.

## Completed Work (Pre-MVP)

These items from the decisions doc and backend TODO are already done:

- **Service layer decomposition** — Waves 0-4 complete. Engine decoupled from API/main. All adapters call service interfaces. (`internal/service/`)
- **Provider expansion** — 8 HTTP providers (Anthropic, OpenAI, Gemini, Mistral, Azure, OpenRouter, OpenZen, Ollama) + 8 CLI adapters (Claude, Codex, Gemini, Copilot, Aider, Junie, Kiro, Qwen)
- **Plugin system beta** — Config, connectors, hooks, events, pre-hooks, widget registration, generator, guide
- **Multi-session presence** — Multiple simultaneous streams, `session_archived` event, CLI active throttle
- **Artifacts** — Auto-detect from tool calls, drawer UI, inline chips, upload/drag-drop
- **Small backend items** — Utility model from DB settings, agent_id in session creation

---

## Phase 0 — Cleanup & Foundation

**Depends on:** Service layer (complete)
**Goal:** Clean up service layer leftovers and address anti-patterns that will get harder to fix later.

### 0.1 Export cleanup
- Delete `internal/chat/exports.go` (6 thin aliases)
- Rename the 6 original unexported functions to exported: `ErrorEvent`, `ErrorEnvelopeDelta`, `ClassifyError`, `BuildKBEnvelope`, `BuildTicketConfirmationEnvelope`, `LogStructuredWarnings`
- Update all callers in `internal/service/` to import from `chat` directly
- **Verify:** `go build` + `go test` clean

### 0.2 Consolidate duplicated helpers
- Identify helpers duplicated between `internal/service/` and `internal/chat/`: `extractIntent`, `buildToolCatalog`, `filterToolsByAllowlist`, `isCLIProvider`, `isPTYProvider`, `inferProvider`, `parseAgentConstraints`, `truncateStr`
- For each: either export from `chat` and import in `service`, or move to a shared internal package if the dependency direction is wrong
- Delete the duplicates
- **Verify:** `go build` + `go test` clean

### 0.3 Hardcoded paths and secrets
- `cmd/nanite/main.go` `setupMCPServers()`: Replace hardcoded absolute paths (`~/go/bin/engine`, `~/Projects-apps/hadron/bin/hadrond`) with config-driven MCP server definitions
- Remove hardcoded Cortex MCP token fallback — require env var or config
- **Verify:** `nanite serve` starts with config-driven MCP servers

### 0.4 Version string
- Create a central `internal/version/version.go` with `var Version = "0.3.0"` (or appropriate)
- Wire into `server.go:handleHealth`, CLI `--version` flag, and any other version references
- Use `ldflags` to inject git SHA at build time

### Frontend (Phase 0)
- No frontend work in this phase

---

## Phase 1 — Tool System & Broker vNext

**Depends on:** Phase 0
**Decisions doc:** §2 (Tool System), §3 (Tool Broker vNext)
**Goal:** Formalize tool metadata and upgrade the broker from simple selection to progressive resolution with decision logging.

### 1.1 Tool interface & metadata
- Define `Tool` interface in new `internal/tool/` package (greenfield):
  - `Name()`, `Description()`, `Category()`, `InputSchema()` (JSON Schema)
  - `Call(ctx, input, execCtx) (*ToolResult, error)`
  - `IsConcurrencySafe(input)`, `IsReadOnly(input)`, `IsDestructive(input)` — input-dependent flags
  - `DefaultPermissions()`, `ValidateInput()`, `Tags()`
- Builder pattern: `NewTool(name, desc, ...ToolOption)` with `WithCategory()`, `WithSchema()`, etc.
- Categories: `core-io`, `search`, `mcp`, `agent`, `session`, `context`, `mode`
- **Verify:** Compile-time interface checks, unit tests for builder

### 1.2 YAML tool definitions
- Tool loader discovers `.nanite/tools/*.yaml` (project) and `~/.nanite/tools/*.yaml` (user)
- Parses JSON Schema from YAML, wraps execution (shell subprocess or Hadron blueprint)
- Registers as `Tool` with `source: "yaml"`
- **Verify:** Create a sample YAML tool, load and execute it

### 1.3 Broker progressive resolution (Layers 1-3)
- Greenfield broker in `internal/tool/broker/` (or extend `internal/toolclient/`)
- Layer 1 (Explicit): Caller specifies tools, active skill bindings, agent config
- Layer 2 (Rule-Based): Project rules (`.nanite/broker.yaml`), user rules (`~/.nanite/broker.yaml`), MCP server tool sets, context mode rules
- Layer 3 (Fast Classifier): Score signals (envelope metadata, agent config, session context, mode) → map to mode preset → tool set
- Rule format per decisions doc §3 (match patterns, intent tags, priority, presets, always_available)
- Fallback: top-N by relevance + always-available set
- **Verify:** Unit tests for each layer, integration test for full resolution chain

### 1.4 Broker decision logging
- `broker_decisions` SQLite table: `id`, `session_id`, `intent`, `layer_reached`, `selected_tools` (JSON), `signals` (JSON), `created_at`
- Migration file for new table
- API endpoint: `GET /api/broker/decisions?session_id=X`
- **Verify:** Decision logged on every tool selection, queryable via API

### 1.5 Migrate existing tools to new interface
- Wrap existing MCP tools, dev tools, self tools to implement new `Tool` interface
- Existing `toolclient.SelectTools()` delegates to new broker
- Old broker code removed once migration verified
- **Verify:** All existing tool flows work unchanged, `go test` clean

### Frontend (Phase 1)
- Broker decision inspector (debug panel) — shows layer reached, signals, selected tools per turn
- YAML tool editor (stretch — could be post-MVP)

---

## Phase 2 — Context Window & Compaction

**Depends on:** Phase 1 (tool metadata needed for tool slot budgeting)
**Decisions doc:** §4 (Slot Architecture), §5 (Compaction), §15 partial (operation-specific model selection for summaries)
**Goal:** Replace flat context assembly with slot-based architecture. Add summary compaction with cheap model.

### 2.1 Slot architecture
- Greenfield `internal/context/` package (or extend `internal/service/context.go`)
- `ContextSlot` struct: Name, Content, TokenCount, CacheKey (SHA-256), Priority, MaxTokens, Flags
- `ContextWindow` struct: Slots map, TotalBudget, UsedTokens, CacheHits, PrevHashes
- Slot ordering: System → Memory → Agent → Rules → Tools → Session → Context → Conversation
- Dynamic budget: static slots allocated first, conversation gets remainder, total = provider window * 0.80
- **Verify:** Unit tests for budget allocation, slot assembly

### 2.2 Cache key system
- SHA-256 hash per slot per turn
- Compare against previous turn: unchanged → mark cacheable
- Provider adapter interprets: Anthropic uses `cache_control: 'ephemeral'`, others skip
- **Verify:** Cache hit/miss tracking works across turns

### 2.3 Integrate with ChatService
- Replace `ContextService.AssembleContext()` internals with slot-based assembly
- `ContextWindow.Assemble()` produces the `[]provider.ContentBlock` the provider expects
- Existing ContextService interface unchanged — slot architecture is internal
- **Verify:** Send messages, verify streaming works, context assembly correct

### 2.4 Summary compaction
- Operation-specific model selection: `Registry.ModelForOperation("summarization")` returns cheapest available model
- If only one model configured, use it; if multiple, prefer haiku-class
- Compaction identifies oldest messages exceeding budget → summarizes with cheap model → replaces span
- Compaction escalation: drop Context enrichment → summarize Conversation → strip tool blocks from non-tool spans → reduce Tools slot
- **Verify:** Compaction triggers at budget threshold, summary replaces messages correctly

### 2.5 Compaction events
- `PreCompact` event: payload with messages being compacted, session context, reason
- `PostCompact` event: payload with summary, tokens saved, slot state
- Plugins can subscribe (foundation for memory extraction in post-MVP)
- **Verify:** Events fire, plugin hooks receive payloads

### Frontend (Phase 2)
- Slot inspector debug panel (shows slot names, token counts, cache status per turn)
- Compaction indicator in chat (visual marker where summary replaced messages)

---

## Phase 3 — Permission & Approval

**Depends on:** Phase 1 (tool metadata: `isDestructive`, `isReadOnly`)
**Decisions doc:** §6 (Permission & Approval Model)
**Goal:** Add per-invocation permission gating with deterministic rules, approval UX, and yolo mode.

### 3.1 Rule engine
- New `internal/permission/` package (greenfield)
- Rule format: tool pattern + input pattern → behavior (deny/ask/allow)
- Rule sources in priority order: session → agent config → project (`.nanite/permissions.yaml`) → user (`~/.nanite/permissions.yaml`)
- Evaluation: deny > ask > allow, first match wins within same priority
- Permission modes: `default`, `accept-edits`, `plan` (read-only), `yolo`
- **Verify:** Unit tests for rule matching, priority ordering, mode behaviors

### 3.2 Three approval scopes
- "Allow once" — this invocation only, not persisted
- "Allow for this session" — stored in session state, cleared on session end
- "Always allow for this project" — written to `.nanite/permissions.yaml`
- **Verify:** Each scope persists/clears correctly

### 3.3 Yolo mode
- Per-session: toggle in session state (GUI toggle or API call)
- Per-project: `mode: yolo` in `.nanite/permissions.yaml`
- Skips all permission prompts when active
- **Verify:** Yolo bypasses permission checks at both scopes

### 3.4 API approval flow
- Tool hits "ask" → API returns `202 Accepted` with `{ approval_request_id, tool, input, reason }`
- Same event pushed on session SSE stream
- Client responds via `POST /sessions/{id}/approvals/{request_id}` with `{ decision, scope }`
- Timeout: configurable (default 60s) → default deny
- **Verify:** Full round-trip: tool triggers ask → SSE event → client responds → tool proceeds/blocked

### 3.5 Wire into chat loop
- Insert permission check between tool selection and tool execution in `chat_generate.go`
- `CheckPermissions(tool, input, session)` → allow/deny/ask
- On "ask": pause execution, emit approval request, wait for callback or timeout
- On deny: emit `PermissionDenied` event, skip tool, feed denial back to LLM
- **Verify:** End-to-end flow with destructive tool triggering approval

### Frontend (Phase 3)
- Permission approval component in chat (inline card with Allow Once / Allow Session / Allow Project buttons)
- Yolo mode toggle in session settings
- Permission rules editor in settings (`.nanite/permissions.yaml` management)

---

## Phase 4 — Chat Loop Hardening ✅

**Depends on:** Phase 2 (slot-aware compaction), Phase 3 (permission continuation)
**Decisions doc:** §7 (Chat Loop & Streaming Architecture)
**Goal:** Formalize the chat loop with named continuation sites, parallel tool execution, and layered iteration control.
**Completed:** 2026-04-02
**Plan:** `~/.claude/plans/quirky-cooking-blum.md`

### 4.1 Named continuation sites — DONE
- `ContinueSite` type with 7 named constants in `internal/service/chat_loop_state.go`
- `continueWith(site, reason)` method logs site + reason at each continuation point
- Active sites: `CONTINUE_TOOL_RESULTS` (after tool execution), `CONTINUE_COMPACTION` (after budget reduction), `CONTINUE_PERMISSION` (after approval)
- Future sites labeled with TODO comments: `CONTINUE_RECOVERY`, `CONTINUE_AGENT_RETURN`, `CONTINUE_HOOK_MODIFIED`, `CONTINUE_MODE_CHANGE`

### 4.2 Parallel tool execution — DONE
- `preCheckTools()` evaluates permission + blocked status before execution
- `executeToolBatch()` partitions tools: concurrent-safe via `sync.WaitGroup`, serial one at a time
- `postProcessToolResults()` handles stuck loop detection, truncation, envelopes, artifacts in original order
- Concurrency safety inferred via `ToolMetaInfo.IsConcurrencySafe` (read/search/fetch → safe, write/edit/bash → unsafe)
- New file: `internal/service/chat_tool_executor.go`

### 4.3 Layered iteration control — DONE
- `loopState` struct consolidates 12+ scattered loop variables into `internal/service/chat_loop_state.go`
- `shouldStop()` checks 5 layers: consecutive failures (cap=3), idle timeout (15min), max turns (25), hard ceiling (100), retry budget
- `recordToolCall()` tracks per-tool call counts + consecutive failures
- `AgentConstraints` extended: `MaxTurns`, `HardCeiling`, `ConsecutiveFailCap`, `IdleTimeoutSeconds`, `DebugMode`
- `ToolMetaInfo` extended: `IsConcurrencySafe`, `MaxIterations`
- Old `maxToolIterations=10` constant removed

### 4.4 Turn snapshots (debug mode) — DONE
- `TurnSnapshot` / `ToolCallSnapshot` structs with `captureSnapshot()` / `captureSnapshotWithTools()` methods
- Gated by `AgentConstraints.DebugMode` — zero overhead when off
- Snapshots serialized to `execution_metrics.debug_snapshots` (JSON blob)
- Migration: `023_add_debug_snapshots.sql`

### Frontend (Phase 4)
- Turn snapshot viewer (debug panel — shows continuation path, tool calls, timing)
- Iteration limit warning in chat (when approaching max turns)

---

## Phase 5 — Agent System ✅

**Depends on:** Phase 1 (tool bindings), Phase 3 (permission modes per agent)
**Decisions doc:** §8 (Agent System)
**Goal:** Move agent definitions from DB-only to MD-based file format as source of truth. DB stores runtime state only.
**Completed:** 2026-04-03
**Plan:** `~/.claude/plans/spicy-munching-pillow.md`

### 5.1 Agent file format — DONE
- `internal/agent/parser.go`: `Definition` struct with YAML frontmatter + markdown system prompt body
- Fields: `name`, `slug`, `description`, `model`, `tools` (allowlist), `permissionMode`, `maxTurns`, `skills`, `mcpServers`, `memory`, `effort`, `isolation`, `tags`, `icon`, `avatar`, `directories`, `constraints`, inline `modes`
- Cross-compatible with Claude Code agent format where fields overlap
- `ParseMD()` / `ParseMDFile()` with frontmatter delimiter parsing

### 5.2 Agent loader & discovery — DONE
- `internal/agent/discovery.go`: `Discover()` scans 6 locations (first slug wins):
  1. CLI `--agent` flag
  2. `.nanite/agents/` (project)
  3. `~/.nanite/agents/` (user)
  4. `plugins/{name}/agents/` (plugin-provided)
  5. `.agentrc/agents/` (agentrc ecosystem)
  6. `.claude/agents/` (Claude Code ecosystem)
- Silent fallback on missing directories, warnings on parse errors
- DB stores only session-agent bindings and runtime overrides

### 5.3 Built-in default agent — DONE
- `internal/agent/builtin/default.md` + `embed.go`: one embedded "Default" agent via `go:embed`
- Minimal: no tool restrictions, no modes, no MCP servers
- Appended as lowest priority (any user/project/plugin agent overrides it)
- **Decision:** Ship 1 built-in agent only. Example agents (code, research, task) deferred to a future "setup" phase.

### 5.4 Seed.go cleanup — DONE
- Removed all 5 agent definitions from `Seed()` (Mentat, Developer, Researcher, Orchestrator, Demo Presenter)
- Removed `SeedAgentSkillBindings()` and its call from `main.go`
- Removed agent mode seeding
- Kept `SeedProviders()` / `SeedWorkspace()` / `SeedBuiltinSkills()` / `SeedBuiltinPromptTemplates()` as-is
- Provider/model YAML extraction deferred (works fine as-is)

### 5.5 Worktree isolation — DEFERRED
- `isolation: worktree` accepted in frontmatter but not yet implemented at runtime
- Deferred to a future phase

### 5.6 AgentService migration — DONE
- `internal/agent/convert.go`: `ToProfile()` / `ToModes()` convert file definitions to `store.AgentProfile`
- Deterministic IDs: `file-{slug}` (stable across restarts, no UUID collision)
- `AgentService.List()`: file-based agents first, then DB agents (slug dedup)
- `AgentService.Get()` / `GetBySlug()`: check file defs first, fall back to DB
- `AgentService.ResolveForSession()`: fallback changed from `mentat-001` → `file-default`
- `internal/service/container.go`: wires `agent.Discover()` + `builtin.DefaultAgent()` into service
- API handlers `handleListAgents` / `handleGetAgent` routed through service layer
- Added file-based source values to `agentvalidation` valid sources

### Frontend (Phase 5)
- Agent file editor (read/write MD files via API)
- Agent picker shows source (file location) and agent metadata
- Agent mode switcher reflects modes from agent definition

---

## Phase 6 — Skill System ✅

**Depends on:** Phase 5 (agent definitions reference skills)
**Decisions doc:** §9 (Skill System)
**Goal:** Implement Agent Skills spec compatible skills with discovery, execution, and broker integration.
**Completed:** 2026-04-03

### 6.1 Skill file format — DONE
- `internal/skill/parser.go`: `Definition` struct with YAML frontmatter + markdown prompt body
- Fields: `name`, `slug`, `description`, `argument-hint`, `allowed-tools`, `model`, `effort`, `context` (inline/fork), `tags`, `broker-hints`
- Cross-compatible with Agent Skills spec (agentskills.io)
- `ParseMD()` / `ParseMDFile()` with frontmatter delimiter parsing

### 6.2 Skill loader & discovery — DONE
- `internal/skill/discovery.go`: `Discover()` scans 5 locations (first slug wins):
  1. `.nanite/skills/` (project)
  2. `~/.nanite/skills/` (user)
  3. `.agentrc/skills/` (agentrc ecosystem)
  4. `.claude/skills/` (Claude Code ecosystem)
  5. `plugins/{name}/skills/` (plugin-provided)
- `internal/skill/context.go`: `ResolveDynamicContext()` replaces `` !`command` `` markers with subprocess output
- Silent fallback on missing directories, warnings on parse errors

### 6.3 Skill execution — PARTIAL
- Inline/fork execution mode stored in definition (`context: "inline"` or `"fork"`)
- Skill-tool bindings via `allowed-tools` field
- Broker hints via `broker-hints` field
- **Deferred:** Runtime execution wiring (injecting skill prompt into session context, fork session creation) — needs chat loop integration in Phase 7

### 6.4 Slash command integration — DONE
- `chat.CommandRegistry.RegisterSkillCommand()`: skills registered as `/slug [args]` commands
- Category "skill", source "file", argument hint wired
- All file-based skills auto-registered at container startup via `RegisterSkillCommands()`

### 6.5 Service layer & seed cleanup — DONE
- `internal/service/skill.go`: `SkillService` interface (Get, GetBySlug, List, ListBySource, Create, Update, Delete, GetDefinition, ListDefinitions)
- File-based primary, DB fallback (same pattern as AgentService)
- Deterministic IDs: `file-{slug}` via `internal/skill/convert.go`
- `SeedBuiltinSkills()` removed from `main.go`
- 8 built-in skills as embedded `.md` files via `internal/skill/builtin/embed.go`
- API handlers routed through SkillService
- `?source=` query param on `GET /api/skills`

### 6.6 Tests — DONE
- 18 tests: parser (5), discovery (4), convert (4), dynamic context (4), slug helpers (1)
- `go build` + `go vet` + `go test` clean (pre-existing `TestAuthMiddlewareEnabled` only)

### Frontend (Phase 6) — DONE
- Skill browser with source filter pills (matches agent UI pattern)
- `SourceBadge` component reused from agents, labels extended for file-based sources
- Category dropdown + source pills, combined filtering
- Source badge on each skill card

---

## Phase 7 — Slash Commands & Polish ✅

**Depends on:** Phase 5 (agent system), Phase 6 (skills as commands)
**Decisions doc:** §3a (Slash Commands from backend TODO)
**Goal:** Round out the command system, fix remaining anti-patterns, final polish pass.
**Completed:** 2026-04-04

### 7.1 Slash commands — DONE
- `/status`, `/providers`, `/export`, `/search` — server-side handlers in `commands_builtin.go`
- `/mode` — client-side command (agent category), added alongside `/agent` and `/model`
- 12 builtin commands + N skill-based commands registered at startup
- Fragments v1 port reviewed: `/clear`, `/import`, `/undo` skipped (not needed for beta)

### 7.2 Anti-pattern fixes — DONE
- **Migration ordering** — Replaced hardcoded file list with `fs.ReadDir()` on embedded FS. New `.sql` files auto-discovered.
- **Envelope sync validation** — `TestEnvelopeRegistrySync` reads frontend registry, fails if backend types are missing. Skips gracefully without UI tree.
- **Nil check consistency** — List endpoints (`handleListConnectors`, `handleVolonListSprints/Tasks/Backlog`) return empty results when deps nil. Action endpoints return "not configured" errors.

### 7.3 Context & debug polish — DONE
- **Real tool token costs** — `context_breakdown.go` queries `execution_metrics` for actual tool token usage via `GetSessionToolTokenSummary()`. Falls back to 50-token estimate when no metrics exist.
- **Slot inspector endpoint** — `GET /api/debug/slots?session_id=X` in `debug_slots.go`. Returns slot names, token counts, cache status from debug snapshots.
- **Snapshot field alignment** — Fixed `continue_site` → `site`, `Duration` → `DurationMs` (float64), added `MaxTurns` and `Parallel` fields. Updated `chat_generate.go` and tests.

### 7.4 Test coverage audit — DONE
- **Fixed `TestAuthMiddlewareEnabled`** — rebrand leftover: tests used `MENTAT_AUTH_*` env vars, middleware reads `NANITE_AUTH_*`.
- **MCP tool count test** — already resilient (at-least semantics), no change needed.
- **18 new tests**: 8 command tests (`commands_test.go`), 10 skill service tests (`skill_test.go`)
- **`go test ./...` — zero failures**

### Frontend (Phase 7) — PARTIAL
- Skill commands auto-registered in slash command autocomplete
- Source filter pills on skill browser (done in Phase 6)
- Envelope sync validated via test (not build-time, but equivalent coverage)
- Full UI polish pass deferred to post-MVP

---

## Phase Summary

| Phase | Scope | Decisions Doc | Key Deliverable |
|-------|-------|--------------|-----------------|
| 0 | Cleanup & Foundation | — | Clean service layer, no hardcoded paths |
| 1 | Tool System & Broker | §2, §3 | Formal tool interface, progressive broker, decision logging |
| 2 | Context & Compaction | §4, §5, §15 | Slot architecture, summary compaction, cheap model selection |
| 3 | Permission & Approval | §6 | Rule engine, 3 scopes, yolo, API approval flow |
| 4 | Chat Loop Hardening | §7 | Continuation sites, parallel tools, iteration limits |
| 5 | Agent System | §8 | MD-based agents, file discovery, seed cleanup |
| 6 | Skill System | §9 | Agent Skills spec, discovery, broker integration |
| 7 | Slash Commands & Polish ✅ | §3a, misc | New commands, anti-pattern fixes, test coverage |

### Dependency Graph

```
Phase 0 (cleanup)
    │
    ▼
Phase 1 (tools & broker)
    │
    ├──────────────────┐
    ▼                  ▼
Phase 2 (context)    Phase 3 (permissions)
    │                  │
    └────────┬─────────┘
             ▼
Phase 4 (chat loop)
             │
             ▼
Phase 5 (agents)
             │
             ▼
Phase 6 (skills)
             │
             ▼
Phase 7 (commands & polish)
```

Note: Phases 2 and 3 can run in parallel (separate sessions) after Phase 1 completes. Phase 4 needs both. Phases 5 and 6 are sequential. Phase 7 is the final polish pass.

### Frontend Work Stream

Frontend items are listed per-phase above. They can be executed in parallel by frontend agents in separate sessions. The frontend work stream depends on the corresponding backend phase being complete (or at least API-stable).
