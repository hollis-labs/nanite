# Nanite Post-MVP — Phased Plan

**Created:** 2026-04-04
**Status:** Active
**Source:** [vnext-backlog.md](vnext-backlog.md)
**MVP:** [vnext-mvp.md](vnext-mvp.md) (Phases 0-7 complete)

## Guiding Principles

Same as MVP:
- Consult before architecture decisions
- Greenfield modules alongside old code, migrate when ready
- go build + go vet + go test clean after every task
- Always use cerberus_rebuild for deployment
- Quality over speed. Polished software, maintainable patterns.
- Brand package: use brand.* constants, never hardcode app name/identity

Additional:
- **Desktop-first.** Target is a WAILS desktop app. All dependencies must be embeddable, no external services required at runtime.
- **Opt-out over opt-in.** Memory, tools, and capabilities should be available by default. Users/agents disable what they don't need.

---

## Phase A — Quick Wins ✅ (parallel, no dependencies)

### A1: MCP Config Import/Export ✅
**Scope:** Import `.mcp.json` (Claude Code format) into Nanite's DB-based MCP server config. Export Nanite config as `.mcp.json`.

**Work items:**
- `nanite mcp import <path>` CLI command — reads Claude Code MCP config, creates DB records via existing `CreateMCPServer` store methods
- `nanite mcp export [path]` CLI command — generates `.mcp.json` from DB records
- GUI import button (upload or file path)

**Effort:** 1 session

### A2: Token Breakdown ✅
**Scope:** Distinguish tool call tokens vs user content in usage tracking.

**Work items:**
- Add `tool_input_tokens` column to `token_usage` table (migration)
- Parse per-block token data from provider usage responses (Anthropic provides this)
- Update `RecordUsage()` to track tool vs content tokens separately
- Update `SessionUsageSummary` to include the breakdown
- Frontend: tool vs content split in TokenUsageWidget
- Foundation: Phase 7.3 already added `GetSessionToolTokenSummary()`

**Effort:** 1 session

---

## Phase B — Infrastructure ✅ (parallel pair)

### B1: Plugin/Event Enhancements ✅
**Scope:** Align hooks with Claude Code lifecycle, per-tool loadType.

**Decisions (2026-04-04):**
- **Hook naming:** Mirror Claude Code exactly (PreToolUse, PostToolUse, Notification, SessionStart, SessionEnd)
- **HTTP hooks:** Skip. Desktop app context, no external webhook consumers needed.
- **loadType override scope:** Session > Project > Agent (consistent with existing override patterns)

**Work items:**
- Rename/alias existing event types to match Claude Code hook names
- Map at the boundary: internal events keep Nanite names, plugin/hook API uses Claude Code names
- Per-tool `loadType` in plugin manifest: `auto` (default) vs `opt-in`
- loadType override chain: session config > project config (`.nanite/plugins.yaml`) > agent config > manifest default
- User override UI in settings for tool load preferences

**Effort:** 1 session

### B2: Multi-Agent Orchestration ✅
**Scope:** Worker spawning, coordination state, task tracking, worktree isolation.

**Decisions (2026-04-04):**
- **Coordination store:** Badger KV (embedded, concurrent-write-safe, pure Go, no CGO). SQLite doesn't handle multi-agent write concurrency. Badger handles high-throughput concurrent writes from many goroutines, supports TTL for ephemeral state.
- **Task tracking:** Built into core service layer (not a plugin). Useful for any agent type, not dev-specific.
- **Worker model:** Both full agent sessions (own chat loop + context window) and lightweight tool-execution-only workers. Used strategically based on task complexity.
- **Worktree isolation:** Implement now at runtime. `isolation: worktree` in agent frontmatter (accepted since Phase 5) triggers git worktree creation for the worker session.

**Work items:**

#### Coordination Store
- Add `github.com/dgraph-io/badger/v4` dependency
- `internal/coordination/` package: Store interface, Badger implementation
- Key patterns: agent heartbeats (`agent:{id}:heartbeat`), tool locks (`lock:{tool}:{session}`), shared state (`state:{key}`)
- TTL for ephemeral keys (heartbeats: 30s, locks: 60s)
- Graceful degradation: if Badger fails to open, log warning and disable multi-agent features (don't block startup)

#### Task Tracking
- `internal/task/` package: Task struct (id, title, status, assignee, parent, metadata, timestamps)
- TaskService in service layer: Create, Update, List, Get, Assign, Transition
- Status flow: pending → in_progress → completed/failed/canceled
- Parent-child relationships for subtask decomposition
- Badger-backed for real-time state, with periodic SQLite snapshots for persistence across restarts
- API endpoints: CRUD + assign + transition + list-by-session + list-by-agent

#### Worker Spawning
- `internal/worker/` package: Manager, Worker interface
- Full worker: spawns a new ChatService session with its own agent, context window, tool set
- Light worker: executes a single tool call or tool batch, returns results, no LLM loop
- Manager tracks active workers, enforces concurrency limits (configurable)
- Worker lifecycle: spawn → run → report → cleanup
- Communication: results written to Badger, parent session polls or subscribes

#### Worktree Isolation
- `internal/worktree/` package: Create, Cleanup, List
- `git worktree add` for worker sessions with `isolation: worktree`
- Working directory scoped to worktree for all tool calls in that session
- Cleanup on worker completion (or on next startup for orphaned worktrees)
- Agent `directories` field constrains tool access within the worktree

**Effort:** 2 sessions (coordination + task in session 1, workers + worktree in session 2)

---

## Phase C — Memory (after B1)

### C1: Memory & Continuity
**Scope:** MemoryService, Cortex integration, memory extraction, memory tools, dream process.

**Decisions (2026-04-04):**
- **Cortex dependency:** Check what Cortex changes are needed first. If type/view registry changes required, create a prompt for the Cortex agent to handle.
- **Extraction triggers:** Both PostCompact hook AND per-turn observer for explicit triggers ("remember this", corrections, preferences).
- **Memory tools:** Available to all agents by default. Opt-out model — users/agents can disable memory if needed.
- **Dream process:** Avoid tight coupling to external Hadron service. Evaluate embedding Hadron as a Go library import (direct function calls, no subprocess). This makes the desktop app fully self-contained AND opens up workflow execution as a core capability for any agent. If Hadron library API isn't clean enough yet, start with a simple in-process consolidation loop and wire Hadron later.

**Work items:**

#### Cortex Integration
- Check Cortex type registry for `memory/*` content types (user, feedback, project, reference, chat, agent)
- Check Cortex view registry for memory views (memory_chat, memory_code, memory_plan, memory_task)
- If changes needed: create Cortex agent prompt for the additions
- `internal/memory/cortex.go`: Cortex-backed MemoryStore implementation

#### MemoryService
- `internal/memory/` package: MemoryService interface (Save, Query, AssembleSlot, Extract, Consolidate)
- Memory types: user, feedback, project, reference, chat, agent (same as Claude Code memory categories)
- Local fallback store (file-based or SQLite) when Cortex is unavailable
- Populate Memory slot from Cortex/local in ContextWindow assembly

#### Extraction
- PostCompact hook subscriber: extract memories from compacted conversation spans
- Per-turn observer: detect explicit memory triggers in user messages
- Classification: determine memory type from content (user preference, project fact, feedback, etc.)
- Dedup: check existing memories before saving (exact match + semantic similarity if available)

#### Memory Tools
- `memory_read` / `memory_write` MCP tools registered by default for all agents
- `memory_search` tool for semantic/keyword search across memory store
- Agents with `memory: disabled` in frontmatter skip memory tool registration

#### Dream Process (minimal)
- In-process consolidation: periodic (configurable interval, default 24h)
- Merge duplicate memories, expire stale entries, update outdated facts
- Wire Hadron as Go library later for full workflow-based consolidation

**Effort:** 2 sessions (service + extraction in session 1, tools + dream in session 2)

**Dependency check:** Cortex agent may need to run first for type/view registry changes.

---

## Phase D — Claude Code Integration (after B1)

### D1: Claude Code Integration
**Scope:** MCP SSE transport, Channel capability, permission relay, launch helpers.

**Order:** SSE transport → Channel protocol → permission relay → CLI helpers

**Work items:**

#### MCP SSE Transport
- Add SSE transport to `internal/mcpserver/` (alongside existing stdio)
- `nanite mcp --transport sse --port 8091` flag
- JSON-RPC over SSE (server→client) + HTTP POST (client→server)

#### Channel Capability
- `claude/channel` capability declaration in MCP server info
- `notifications/claude/channel` event format for bidirectional messaging
- Reply tool for Claude Code → Nanite commands

#### Permission Relay
- `--permission-prompt-tool` — Nanite exposes MCP tool for Claude Code approval prompts
- Maps to Nanite's Phase 3 permission engine
- Approval UI forwarding: Claude Code prompt → Nanite SSE → frontend approval card → response

#### CLI Helpers
- `nanite claude-launch` — wraps all flags for launching Claude Code with Nanite as MCP server
- `nanite claude-agents` — generates `--agents` JSON from Nanite agent definitions
- `nanite claude-config` — generates `.mcp.json` for Claude Code pointing to Nanite
- `--append-system-prompt` compact context pointer generation

**Effort:** 2 sessions (SSE transport + Channel in session 1, permission relay + CLI helpers in session 2)

---

## Dependency Graph

```
A1 (MCP Import)  ──┐
A2 (Token)       ──┤── parallel, no deps
                    │
B1 (Plugin/Event)──┐├── parallel pair
B2 (Multi-Agent) ──┘│
                    │
C1 (Memory)      ───┤── after B1
D1 (Claude Code) ───┘── after B1
```

Total: ~10 sessions across 4 phases.
