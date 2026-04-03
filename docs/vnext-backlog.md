# Conduit vNext — Post-MVP Backlog

**Created:** 2026-04-02
**Status:** Reference
**Source:** [conduit-vnext-decisions.md](/Users/chrispian/Projects-apps/agent-workspaces/exploration/conduit-vnext-decisions.md)
**MVP doc:** [vnext-mvp.md](vnext-mvp.md)

## Purpose

Items deferred from the MVP scope. These build on top of the service layer and MVP features. Organized by topic with decision status and dependencies noted. Priority is indicative — revisit when MVP ships.

---

## High Priority (first post-MVP wave)

### Token Breakdown: Tool Call vs User Content
**Decision status:** Decided — post-MVP enhancement.

The Token Usage widget correctly tracks cumulative input/output/cache tokens (Anthropic's API includes tool_use blocks in InputTokens). However, there's no way to distinguish how much of InputTokens came from tool calls vs. user content. Add a separate `tool_input_tokens` field to the usage tracking so the Token Usage widget can show a breakdown (e.g., "Input: 12,400 tokens — 8,200 content, 4,200 tool calls").

**Work items:**
- Track tool_use block token costs separately in `RecordUsage()` (requires parsing the provider's per-block token breakdown if available, or estimating from tool input JSON size)
- Add `tool_input_tokens` column to `token_usage` table
- Update `SessionUsageSummary` to include the breakdown
- Frontend: add tool vs content split in TokenUsageWidget

### Memory & Continuity (§16)
**Decision status:** Mostly decided. Q16.3 (dream process) deferred.

Conduit owns memory lifecycle (extraction, classification, injection). Cortex owns storage. The Memory slot (§4, built in MVP Phase 2) gets populated from Cortex using context-aware views (mode-dependent: chat, code, plan, research, task).

**Work items:**
- Add `memory/*` content types to Cortex type registry (6 types: user, feedback, project, reference, chat, agent)
- Add memory views to Cortex view registry (memory_chat, memory_code, memory_plan, memory_task)
- Implement `MemoryService` interface in Conduit (`Save`, `Query`, `AssembleSlot`, `Extract`)
- Populate Memory slot from Cortex in `ContextWindow` assembly (MVP Phase 2 foundation)
- Wire `PostCompact` hook for memory extraction (MVP Phase 2 foundation)
- Add `memory_read` / `memory_write` tools to tool catalog
- Per-turn observer for explicit memory triggers ("remember this", corrections, preferences)
- Dream process (Q16.3): scheduled consolidation, merge duplicates, expire stale — likely Hadron blueprint + sleeping agent

**Dependencies:** MVP Phase 2 (slots, compaction events), Cortex type/view changes

### MCP Config Import/Export
**Decision status:** Deferred to post-MVP (decided 2026-04-02).

Import `.mcp.json` (Claude Code format) into Conduit's DB-based MCP server config. Export Conduit config as `.mcp.json` for Claude Code compatibility.

**Work items:**
- `conduit mcp import ~/.mcp.json` — reads Claude Code MCP config, creates DB records via existing `CreateMCPServer` store methods
- `conduit mcp export` — generates `.mcp.json` from DB records
- GUI import button (upload or path)

**Dependencies:** None (DB-based MCP config already complete with full CRUD API)

### Claude Code Integration (§13)
**Decision status:** Decided.

Conduit as a Channel plugin (MCP SSE with `claude/channel` capability). Permission prompt relay. CLI launch helpers.

**Work items:**
- MCP SSE transport for Conduit's MCP server (alongside existing stdio)
- `claude/channel` capability declaration + `notifications/claude/channel` event format
- Reply tool for Claude Code → Conduit commands
- `--permission-prompt-tool` — Conduit exposes MCP tool for Claude Code approval prompts
- `--append-system-prompt` compact context pointer generation
- `conduit claude-launch` convenience command (wraps all flags)
- `conduit claude-agents` — generates `--agents` JSON from Conduit agent config
- `conduit claude-config` — generates `.mcp.json` for Claude Code
- HTTP hook server: PreToolUse, PostToolUse, SessionStart/End, Notification

**Dependencies:** MVP Phase 3 (permission system), MCP SSE transport

### Multi-Agent Orchestration (§14)
**Decision status:** Decided (Q14.1-Q14.4).

SQLite per session + badger for shared state. Plan/task tracking as core plugin. Adapter pattern for external orchestration (Engine, GitHub, Linear).

**Work items:**
- Badger KV store integration for shared coordination state
- Task tracking core plugin (lightweight local planning + basic task tools)
- Engine adapter for heavy orchestration (ships day 1)
- Worker agent spawning — full agents with optional restrictions via broker rules
- Worktree-per-worker isolation (builds on MVP Phase 5 worktree support)

**Dependencies:** MVP Phase 5 (agent system), MVP Phase 4 (chat loop for agent return continuation)

### Plugin & Event System Enhancements (§17)
**Decision status:** Decided (Q17.1-Q17.3).

Align hooks with Claude Code lifecycle. HTTP hooks for external services. Per-tool `loadType` in plugin manifest.

**Work items:**
- Align hook types with Claude Code: PreToolUse, PostToolUse, SessionStart, SessionEnd, Notification, etc.
- HTTP hook support — external services participate in lifecycle via webhooks
- Per-tool `loadType` in plugin manifest: `auto` (default) vs `opt-in`
- User override for loadType via config (disable auto tools, enable opt-in tools)

**Dependencies:** MVP Phase 1 (tool interface)

---

## Medium Priority

### A2A Messaging & Federation (§10)
**Decision status:** Open. Q10.1-Q10.3 undecided.

**Open questions:**
- Build A2A into Conduit core or keep in Engine + access via MCP?
- Remote federation auth model (API keys? OAuth? mTLS?)
- Remote agent discovery (registry? manual config? mDNS?)

**Work items (once decided):**
- Local A2A: Nexus-style typed messages between sessions/agents, SSE delivery, inbox per agent
- Remote A2A: MCP-based (connect remote Conduit as MCP server), SSE subscription, HTTP hooks
- CLI agent integration: Channels for Claude Code, MCP stdio for others, wrapper translators

**Dependencies:** MCP SSE transport, Claude Code integration

### Session Lifecycle — Pull-In/Pop-Out (§11)
**Decision status:** Decided as post-MVP.

- **Pull-in:** Read Claude Code transcript files (`~/.claude/projects/{project}/{sessionId}/`), reconstruct into StructuredMessage format. `conduit session import --claude-session {id}`. Also support `--output-format stream-json` file import for non-Claude CLIs.
- **Pop-out:** Export session, launch `claude --resume {id}` in configured terminal (macOS Terminal, iTerm2, tmux).
- **Idle timeout:** Default 15min, configurable per session/agent/project. Suspend → serialize state → resume transparently.
- **Claude Agent SDK:** Evaluate for programmatic session management.

**Dependencies:** Claude Code integration, session state serialization

### Provider Abstraction Enhancements (§15)
**Decision status:** Partially decided. Q15.4 deferred.

- **Operation-specific model selection:** MVP Phase 2 adds `ModelForOperation("summarization")`. Extend to: routing, tool selection, classification, embedding, composition.
- **Graceful degradation:** Warn when provider doesn't support a feature (cache_control, extended thinking) rather than hard block.
- **SDK evaluation (Q15.4):** LiteLLM, AI SDK, etc. — deferred, evaluate if provider adapter maintenance becomes burdensome.
- **Claude Agent SDK (Q15.5):** Explore for programmatic agent session management.

**Dependencies:** MVP Phase 2 (model selection foundation)

### Sideloaded Execution (§18)
**Decision status:** Open. Q18.1-Q18.3 undecided.

**Open questions:**
- Core feature or plugin?
- Execution backend: subprocess? Goroutine? Remote (Engine)?
- Status checking: `/tasks` command? Sidebar panel? A2A inbox?

**Concept:** User requests sideloaded task → Conduit spawns isolated execution context → main chat continues → completion notified via SSE/A2A/inbox.

**Dependencies:** Multi-agent orchestration, A2A messaging

### IDE Bridge (§12)
**Decision status:** Decided — defer to post-MVP.

- Use Channels + MCP as bidirectional layer (not custom bridge protocol)
- VS Code / JetBrains extensions deferred
- Permission prompt relay from CLI sessions to GUI via SSE + control channel (decided yes)

**Dependencies:** Claude Code integration (Channels), Permission system

---

## Low Priority / Future

### Always-On / Sleeping Agents (§19)
**Decision status:** Exploring. Q19.1-Q19.3 undecided.

**Patterns to explore:**
- Sleeping agents: registered with triggers (file change, git push, schedule, A2A), wake-process-sleep
- Active agents: long-running, observe events, act/react
- Special agents: ToolBroker, ContextBroker as always-on services with agent interface

**Dependencies:** Multi-agent orchestration, Engine scheduler integration

### Engine Backlog (§20)
Items for Engine, informed by Conduit vNext decisions:

| ID | Item | Conduit Dependency |
|----|------|--------------------|
| E1 | Engine MCP SSE transport | MCP SSE in Conduit |
| E2 | Runtime permission enforcement | Permission rule engine (MVP §3) |
| E3 | Slot-based task context | Slot architecture (MVP §2) |
| E4 | Nexus ↔ Conduit A2A bridge | A2A messaging |
| E5 | Scheduler wakes sleeping agents | Sleeping agents |
| E6 | Bootstrap → slot architecture | Slot architecture (MVP §2) |
| E7 | Quality gates as Conduit plugin | Plugin system |
| E8 | Run records → Cortex memory | Memory system |
| E9 | Shared tool spec + permission libs | Tool system (MVP §1), Permissions (MVP §3) |
| E10 | Cost budget gating | Provider abstraction |
| E11 | Federation protocol | A2A messaging |
| E12 | Sprint/task as context object | Slot architecture (MVP §2) |

---

## Refactoring Backlog (ongoing, do when touching the area)

These are technical debt items best addressed incrementally rather than as a dedicated effort:

### Anonymous struct request bodies
- 45 occurrences across 20 API handler files
- Each handler defines inline `var req struct { ... }` for request parsing
- Blocks: API doc generation (OpenAPI), Go client SDK, consistent validation
- **Approach:** Extract shared request/response types as handlers are modified for other reasons. Not a standalone project.

### agentrc Integration Review (§6b from backend TODO)
- May be superseded by the MD-based agent system (MVP Phase 5)
- Review after Phase 5: does the agentrc ecosystem add value beyond what `.conduit/agents/` provides?
- Options: import agentrc configs, full config loader integration, or AgentSource adapter layer
- Agent framework adapters (CrewAI, AutoGen, LangGraph) — evaluate demand

### Old chat.Engine cleanup
- `internal/chat/engine.go` (2153 lines) still exists but is no longer referenced by API/main
- After MVP Phase 4 (chat loop hardening), evaluate: delete entirely or keep as reference?
- Any logic still needed should have been extracted by service layer + new modules

### Compaction enhancements (deferred from §5)
- **Snip compaction** — Remove middle conversation span, keep recent + system
- **Reactive compaction** — In-flight recovery when provider returns `prompt_too_long`
- Both cleaner to implement on slot boundaries (MVP Phase 2)

---

## Related Engine Tasks

From the decisions doc appendix — existing Engine tasks related to this work:

**Conduit (selected):**
- `TASK-20260320-018` — Tiered structured message format
- `TASK-20260320-087` — Migrate ToolBroker into Nexus
- `TASK-20260320-088` — Migrate ContextBroker into Nexus
- `TASK-20260315-017` — Conduit standalone agent memory
- `TASK-20260315-001` — Plugin loader + registry
- `TASK-20260320-116` — Middleware pipeline executor in plugin host
- `TASK-20260315-013` — A2A inbox plugin

**Cortex (selected):**
- `TASK-20260316-007` — pgvector integration for similarity search
- `TASK-20260314-214` — Context lifecycle (dream process)
- `TASK-20260316-009` — RAG query tool
- `TASK-20260316-024` — Research: Memory Systems
