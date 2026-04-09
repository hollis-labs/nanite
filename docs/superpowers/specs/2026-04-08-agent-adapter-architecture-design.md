# Agent Adapter Architecture Design

**Date:** 2026-04-08
**Status:** Approved
**Scope:** CLI agent adapter system, config override cascade, messaging absorption, .agentrc → .nanite rename

---

## Summary

Nanite becomes a universal agent harness — able to discover, import, and spawn agents from any CLI ecosystem (Claude Code, Codex, Copilot, Gemini, Opencode, and future frameworks like CrewAI). This is achieved through a plugin-per-adapter model with a core interface in `internal/agent/`, a three-layer config override cascade, and absorption of Nexus messaging into Nanite's SQLite store.

The `.agentrc/` convention is renamed to `.nanite/` with `NANITE.md` as the canonical boot prompt. Managed sections in CLI-specific files (CLAUDE.md, AGENTS.md, GEMINI.md) keep everything in sync from a single source of truth.

---

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Nexus | Absorb messaging into SQLite, drop registry/broker/skills, cut dependency | Nanite's native agent store is sufficient; messaging is the only useful piece; PostgreSQL dependency unnecessary |
| Architecture | Plugin-per-adapter, interface in `internal/agent/adapter.go` | Agents are a core concept; plugins implement, not own, the interface; consistent with existing plugin model |
| agentrc treatment | Richest adapter (CLIAgentAdapter + AgentComposer), not privileged | agentrc's role composition is unique value, but it's one adapter among many |
| Config cascade | Agent Base → Project → Session, per-field merge semantics | Matches existing patterns in codebase; provides right level of flexibility |
| Boot prompt ownership | NANITE.md canonical, managed sections in CLI-specific files | Single source of truth; respects existing user content in CLI files |
| Clockwork Manifold | Not coupled — MCP or plugin integration, separate effort | Different purposes (task orchestration vs chat harness); plugin/MCP at boundary |
| Naming | `.nanite/` + `NANITE.md` replaces `.agentrc/` in this repo | Eliminates confusion with Microsoft's agentrc; consistent with CLI conventions (.claude/, .codex/) |
| Sandbox writing | Every adapter writes its files on spawn | Subagents get config files for their specific CLI |
| Future adapters | CrewAI first, then AutoGen/LangGraph (deferred) | CrewAI is most config-driven; interface supports all three |

---

## 1. Core Interfaces

**File:** `internal/agent/adapter.go`

### CLIAgentAdapter

Base interface for all CLI ecosystem adapters.

```go
type CLIAgentAdapter interface {
    // Name returns the adapter identifier (e.g., "nanite-native", "claude", "codex").
    Name() string

    // Discover scans projectDir for agent definitions in this ecosystem's format.
    // Returns normalized Definitions. Called during agent discovery.
    Discover(projectDir string) ([]Definition, error)

    // PopulateSandbox writes CLI-specific config files into sandboxDir
    // so a spawned subagent using this CLI can read its context.
    PopulateSandbox(sandboxDir string, agent AgentProfile, session SandboxContext) error

    // SyncProjectRoot writes/updates managed sections in the project root
    // (e.g., <!-- nanite:start --> block in CLAUDE.md). Called on config change.
    SyncProjectRoot(projectDir string, agents []AgentProfile) error

    // Priority determines discovery order. Lower = checked first.
    Priority() int
}
```

### AgentComposer (Optional Extension)

For adapters that compose agents from multiple sources (roles, skills, context). The nanite-native adapter implements this. Future adapters like CrewAI could also implement it.

```go
type AgentComposer interface {
    CLIAgentAdapter

    // ComposePrompt assembles a system prompt from the adapter's composition
    // model (e.g., roles + skills + context for nanite-native).
    ComposePrompt(agentConfig interface{}, projectDir string) (string, error)

    // ListRoles returns available roles this composer can resolve.
    ListRoles() ([]RoleInfo, error)

    // ListSkills returns available skills this composer can resolve.
    ListSkills() ([]SkillInfo, error)
}
```

### AdapterRegistry

Manages loaded adapters, iterates for discovery and sandbox population.

```go
type AdapterRegistry struct {
    adapters []CLIAgentAdapter  // sorted by Priority()
}

func (r *AdapterRegistry) Register(a CLIAgentAdapter)
func (r *AdapterRegistry) DiscoverAll(projectDir string) ([]Definition, error)
func (r *AdapterRegistry) PopulateAllSandboxes(sandboxDir string, agent AgentProfile, session SandboxContext) error
func (r *AdapterRegistry) SyncAllProjectRoots(projectDir string, agents []AgentProfile) error
func (r *AdapterRegistry) GetAdapter(name string) (CLIAgentAdapter, bool)
```

### Design Notes

- `Discover` returns `[]Definition` (existing type from `internal/agent/parser.go`) — all adapters normalize to Nanite's native format.
- `PopulateSandbox` is called for every registered adapter when spawning — a Claude subagent gets CLAUDE.md but also AGENTS.md.
- `SyncProjectRoot` uses managed-section markers so user content is preserved.
- `Priority()` replaces the hardcoded 6-tier discovery order for adapter-sourced agents. Native discovery tiers 1-4 (CLI flag, `.nanite/agents/`, `~/.nanite/agents/`, `plugins/*/agents/`) remain unchanged in core.
- `SandboxContext` carries session ID, working directory, MCP server config, and any session-level overrides. Defined alongside the adapter interface — wraps existing sandbox population options.

---

## 2. Config Override Cascade

**File:** `internal/agent/override/merge.go`

### Three Layers

```
Layer 1: Agent Base        (from adapter Discover or file-based definition)
Layer 2: Project Override   (from .nanite/config.yaml or project settings)
Layer 3: Session Override   (from session creation or runtime API)
```

### Merge Rules

| Field Type | Rule | Example |
|---|---|---|
| Scalar (model, provider, description) | Last-writer-wins | Session sets `model: claude-opus` → overrides base |
| List (tools, skills, mcpServers, tags, directories) | Union + explicit remove with `-` prefix | `["+deploy", "-code_exec"]` |
| Map (permissions, settings, constraints) | Deep merge, nested scalars are last-writer-wins | `{constraints: {maxTurns: 50}}` merges into base |
| Unset/zero value | Skipped — doesn't override lower layer | Project sets no model → agent base model preserved |

### Override Config Format

Same shape at both project and session layers:

```yaml
# In .nanite/config.yaml under agent_overrides:
agent_overrides:
  "file-researcher":          # by agent ID or slug
    model: claude-opus
    tools: ["+database", "-web_search"]
    constraints:
      maxTurns: 50
  "*":                        # wildcard: applies to all agents
    tools: ["+internal_docs"]
    directories: ["/shared/data"]
```

### Resolution

```go
func Resolve(base AgentProfile, project, session *OverrideConfig) AgentProfile
```

Wildcard `"*"` overrides apply first, then agent-specific overrides on top.

### Storage

Session overrides stored in `session_agent_overrides` table (sessionID + JSON blob), set via API at session creation or mid-session.

---

## 3. Adapter Implementations

All adapters are built-in Nanite plugins in `internal/plugin/builtin/`.

### nanite-native (replaces agentrc-sync)

**Plugin:** `internal/plugin/builtin/adapter-nanite-native/`
**Implements:** `CLIAgentAdapter` + `AgentComposer`
**Priority:** 50

- **Discover:** Reads `~/.nanite/config.yaml` (global roles) + `.nanite/config.yaml` (project agents). Resolves roles from `~/.nanite/roles/`, appends project context. Returns `[]Definition` with `Source: "nanite"`.
- **ComposePrompt:** Concatenates role files + project context. Unique value — no other adapter does composition.
- **PopulateSandbox:** Writes `.nanite/` structure into sandbox (config, roles, context).
- **SyncProjectRoot:** No-op — nanite-native manages its own files.

### Claude Code

**Plugin:** `internal/plugin/builtin/adapter-claude/`
**Implements:** `CLIAgentAdapter`
**Priority:** 60

- **Discover:** Reads `.claude/agents/*.md` — MD with YAML frontmatter (same format as Nanite). Returns `[]Definition` with `Source: "claude"`.
- **PopulateSandbox:** Writes `CLAUDE.md` (compact rules, agent context, envelope spec, `.sandbox/` pointers) + `.mcp.json`. Absorbs current `internal/sandbox/sandbox.go` logic.
- **SyncProjectRoot:** Writes/updates `<!-- nanite:start -->` managed section in project-root `CLAUDE.md`.

### Codex / Copilot

**Plugin:** `internal/plugin/builtin/adapter-codex/`
**Implements:** `CLIAgentAdapter`
**Priority:** 70

- **Discover:** Reads `AGENTS.md` from project root (OpenAI/Copilot agent format).
- **PopulateSandbox:** Writes `AGENTS.md` into sandbox.
- **SyncProjectRoot:** Writes/updates managed section in project-root `AGENTS.md`.

### Gemini

**Plugin:** `internal/plugin/builtin/adapter-gemini/`
**Implements:** `CLIAgentAdapter`
**Priority:** 70

- **Discover:** Reads `GEMINI.md` from project root.
- **PopulateSandbox:** Writes `GEMINI.md` into sandbox.
- **SyncProjectRoot:** Writes/updates managed section in `GEMINI.md`.

### Opencode

**Plugin:** `internal/plugin/builtin/adapter-opencode/`
**Implements:** `CLIAgentAdapter`
**Priority:** 70

- **Discover:** Reads Opencode's MD-based config (exact format to be confirmed during implementation).
- **PopulateSandbox / SyncProjectRoot:** Same pattern.

---

## 4. Messaging Absorption

**Goal:** Port Nexus `messaging` to Nanite's SQLite store, drop Nexus dependency.

### New Table

```sql
CREATE TABLE messages (
    id          TEXT PRIMARY KEY,
    from_agent  TEXT NOT NULL,
    to_agent    TEXT NOT NULL,
    thread_id   TEXT,
    type        TEXT NOT NULL DEFAULT 'message',
    status      TEXT NOT NULL DEFAULT 'unread',
    subject     TEXT,
    body        TEXT,
    metadata    TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX idx_messages_to_agent ON messages(to_agent, status);
CREATE INDEX idx_messages_thread   ON messages(thread_id);
```

**Message types:** message, help_request, directive, status_update, handoff
**Status lifecycle:** unread → read → acknowledged → resolved

### Store Interface

**File:** `internal/store/messaging.go`

```go
type MessageStore interface {
    Send(ctx, input SendMessageInput) (Message, error)
    Inbox(ctx, agentID string, opts InboxOptions) ([]Message, error)
    Thread(ctx, threadID string) ([]Message, error)
    Get(ctx, messageID string) (Message, error)
    Ack(ctx, messageID string) error
    Resolve(ctx, messageID string) error
    UnreadCount(ctx, agentID string) (int, error)
}
```

### Migration Path

1. Add `messages` table via new DDL migration
2. Implement `MessageStore` on existing `store.DB`
3. Update `internal/api/a2a.go` to use `store.MessageStore` instead of `nexus/messaging`
4. Drop `nexusMessageToA2A()` conversion functions
5. Remove `nexus` from `go.mod`

**API consumers see no change** — same HTTP endpoints, same JSON shapes.

---

## 5. NANITE.md & Managed Sections

### Canonical Source

`NANITE.md` in project root — the single place users edit Nanite-specific agent context, rules, and configuration.

### Managed Section Protocol

```markdown
<!-- nanite:start -->
<!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->

[adapter-generated content]

<!-- nanite:end -->
```

**Rules:**
- Content between markers is fully replaced on each sync
- Content outside markers is never touched
- If the file doesn't exist, adapter creates it with just the managed section
- If markers don't exist in an existing file, append at end
- `SyncProjectRoot` is idempotent

### NANITE.md Structure

```markdown
# Project: My App

## Agents
[pointers to .nanite/agents/*.md definitions]

## Tools
[tool availability, load preferences, custom tool docs]

## Rules
[project-specific conventions, constraints, envelope formats]

## Context
[architecture notes, key files, patterns to follow]
```

### Sync Triggers

- Nanite startup (bootstrap)
- Agent config change (adapter discovers changes)
- Explicit user action (future: `/nanite sync` command)

---

## 6. .agentrc → .nanite Rename (In Scope)

This rename applies to Nanite's codebase only. The standalone agentrc repo at `~/Projects-apps/agentrc` is handled separately.

### Changes

| Current | Becomes |
|---|---|
| `.agentrc/config.yaml` | `.nanite/config.yaml` |
| `.agentrc/agents/*.md` | `.nanite/agents/*.md` |
| `.agentrc/boot-prompt.md` | `.nanite/boot-prompt.md` (or NANITE.md absorbs this) |
| `~/.agentrc/roles/` | `~/.nanite/roles/` |
| `~/.agentrc/skills/` | `~/.nanite/skills/` |
| `~/.agentrc/config.yaml` | `~/.nanite/config.yaml` |
| `agentrc-sync` plugin | `nanite-native` adapter plugin |
| `Source: "agentrc"` | `Source: "nanite"` |
| Boot command: "Boot <agent>" reads `.agentrc/` | Reads `.nanite/` |

### Backward Compatibility

If `.agentrc/` exists and `.nanite/` doesn't, the nanite-native adapter reads from `.agentrc/` with a deprecation warning logged. This eases migration for existing projects.

---

## 7. Migration from Current Code

| Current Code | Becomes |
|---|---|
| `internal/plugin/builtin/agentrc/plugin.go` | `internal/plugin/builtin/adapter-nanite-native/` (refactored, adds Composer) |
| `internal/sandbox/sandbox.go` (CLAUDE.md + .mcp.json) | `adapter-claude/` PopulateSandbox method |
| Agent discovery tiers 5-6 in `internal/agent/` | AdapterRegistry iterates all adapters' Discover() |
| Hardcoded 6-tier priority | Adapter Priority() values + native discovery (tiers 1-4 unchanged) |
| `nexus/messaging` usage in `internal/api/a2a.go` | `internal/store/messaging.go` (SQLite) |
| `nexus` dependency in `go.mod` | Removed |

**What stays in core (not moved to adapters):**
- Tiers 1-4 of discovery (CLI `--agent` flag, `.nanite/agents/`, `~/.nanite/agents/`, `plugins/*/agents/`)
- `internal/agent/parser.go` — MD+YAML frontmatter parsing (shared by adapters)
- `internal/sandbox/` — owns sandbox directory lifecycle, delegates content writing to adapters

---

## 8. Deferred Work (Future Sessions)

### Future Adapters: CrewAI, AutoGen, LangGraph

Implement `CLIAgentAdapter` + potentially `AgentComposer`:

- **CrewAI adapter** (first priority) — Reads CrewAI's agent YAML (role, goal, backstory, tools). Composer extension maps CrewAI "crew" concept to Nanite's multi-agent model.
- **AutoGen adapter** — Reads AutoGen agent configs. Limited to declarative config-file definitions (code-defined agents out of scope).
- **LangGraph adapter** — Reads LangGraph graph definitions. Same constraint — declarative config only.

### Frontend: Agent Discovery UI Changes

Bullet points for the frontend agent:

- **Adapter source badges** — Agent list shows source (nanite, claude, codex, etc.) as badge. Data available via `AgentProfile.Source`.
- **Override editor** — UI for project and session overrides. Show cascade: base → project → session → effective. CSS-inspector-style devtools pattern.
- **Sync status** — Indicator showing active adapters, last sync time, discovery errors.
- **NANITE.md preview** — Settings panel showing managed section content with manual "sync now" button.
- **Adapter management** — List of registered adapters with enable/disable toggle.

### Nexus Broker (If Needed)

Capability-based agent matching/scoring (~80 lines of algorithm). Build if multi-agent orchestration needs automated agent selection. The interface supports it — add as a service, not an adapter.
