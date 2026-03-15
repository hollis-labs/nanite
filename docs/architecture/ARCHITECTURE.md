# CONDUIT — Architecture Document

**Version:** 2.0
**Date:** 2026-03-15
**Status:** Active

> CONDUIT is the agent-agnostic chat harness. Mentat is a Special Agent that runs inside it. See ADR-013 for the separation rationale.

---

## 1. System Overview

```
┌─────────────────────────────────────────────────────────────┐
│                     CONDUIT Binary                          │
│                                                             │
│  ┌──────────────────────┐  ┌─────────────────────────────┐  │
│  │   Go API Server      │  │   Embedded React SPA        │  │
│  │                      │  │                             │  │
│  │  /api/sessions       │  │  4-Column Layout            │  │
│  │  /api/messages       │  │  TipTap Composer            │  │
│  │  /api/workspaces     │  │  Zustand + React Query      │  │
│  │  /api/agents         │  │  Envelope Renderers         │  │
│  │  /api/providers      │  │  Widget System              │  │
│  │  /api/stream/:id     │  │                             │  │
│  │  /api/workflows      │  │                             │  │
│  └──────────┬───────────┘  └─────────────────────────────┘  │
│             │                                               │
│  ┌──────────┴───────────┐                                   │
│  │   Core Services      │                                   │
│  │                      │                                   │
│  │  ChatEngine          │◄── Orchestrates turns, context    │
│  │  AgentManager        │◄── Profiles, modes, spawning      │
│  │  ContextBroker       │◄── Scoping, budget, compaction    │
│  │  ProviderRegistry    │◄── Multi-provider streaming       │
│  │  MCPClient           │◄── Tool routing to MCP servers    │
│  │  WorkflowEngine      │◄── Deterministic YAML flows       │
│  │  EnvelopeParser      │◄── Structured response parsing    │
│  └──────────┬───────────┘                                   │
│             │                                               │
│  ┌──────────┴───────────┐                                   │
│  │   SQLite (modernc)   │                                   │
│  │   mentat.db     │                                   │
│  └──────────────────────┘                                   │
└─────────────┬───────────────────────────────────────────────┘
              │
              │  MCP (stdio/HTTP)
              ▼
┌─────────────────────────────────────────────────────────────┐
│              Fragments Engine Services (MCP)                 │
│                                                             │
│  Volon (tasks, sprints)  │  Cortex (context, PCC)          │
│  Hadron (blueprints)     │  Custom MCP servers              │
└─────────────────────────────────────────────────────────────┘
```

## 2. Go Backend Architecture

### 2.1 Package Layout

```
cmd/conduit/
  main.go                    -- entry point, flag parsing, server start

internal/
  server/
    server.go                -- HTTP server, router, middleware
    routes.go                -- route registration
    middleware.go            -- CORS, logging, recovery

  api/
    sessions.go              -- session CRUD handlers
    messages.go              -- message send + SSE stream
    workspaces.go            -- workspace/project CRUD
    agents.go                -- agent profile management
    providers.go             -- provider config
    workflows.go             -- workflow trigger/status
    bookmarks.go             -- bookmark CRUD
    artifacts.go             -- artifact upload/download

  chat/
    engine.go                -- turn orchestration (the core loop)
    context.go               -- context assembly + budget management
    envelope.go              -- envelope parsing + validation
    compaction.go            -- session compaction (LLM summarization)

  agent/
    manager.go               -- agent profile loading, mode switching
    mode.go                  -- mode definitions and prompt assembly
    multi.go                 -- multi-agent session coordination
    spawner.go               -- agent-to-agent session creation

  provider/
    registry.go              -- provider interface + registry
    anthropic.go             -- Anthropic streaming adapter
    openai.go                -- OpenAI streaming adapter (post-MVP)
    ollama.go                -- Ollama adapter (post-MVP)

  mcp/
    client.go                -- MCP client (stdio + HTTP transports)
    registry.go              -- tool discovery from connected servers
    router.go                -- route tool calls to correct server

  workflow/
    engine.go                -- YAML workflow parser + executor
    types.go                 -- workflow, step, input definitions

  store/
    store.go                 -- DB connection, migrations
    sessions.go              -- session queries
    messages.go              -- message queries
    workspaces.go            -- workspace/project queries
    agents.go                -- agent profile queries
    providers.go             -- provider/model queries
    bookmarks.go             -- bookmark queries
    artifacts.go             -- artifact metadata queries
    migrations.go            -- embedded SQL migrations

  stream/
    sse.go                   -- SSE writer (typed events)
    events.go                -- event type definitions
```

### 2.2 Chat Engine (Core Loop)

The chat engine orchestrates a single turn:

```
1. Receive message (POST /api/messages)
   → Persist user message
   → Return message_id immediately

2. Stream response (GET /api/stream/:message_id)
   → Assemble context (ContextBroker)
   → Build system prompt (agent profile + mode + workspace context)
   → Stream to AI provider
   → Parse response for envelopes
   → If tool_use in response:
     a. Route tool call to MCP client
     b. Stream tool progress events
     c. Feed tool result back to provider
     d. Continue streaming (agentic loop)
   → Persist assistant message
   → Prune context if over budget
   → Emit stream_end event
```

**Key design: The primary agent (e.g., Mentat) never executes tools directly.** When it needs work done, it creates a delegation envelope that the chat engine routes to a worker agent session. CONDUIT handles the routing — the agent profile determines the behavior.

### 2.3 Context Broker

```go
type ContextBroker struct {
    budgetPct   float64  // default 0.75
    maxTokens   int      // from model config
}

func (cb *ContextBroker) Assemble(session, workspace, project) []Message {
    // 1. System prompt (agent + mode + workspace context)
    // 2. Session history (with compacted old turns)
    // 3. Pinned/bookmarked context
    // 4. Active task context (if task-scoped)
    // 5. Enforce budget ceiling
}

func (cb *ContextBroker) PruneAfterTurn(messages) []Message {
    // Compact old tool results to "[compacted]"
    // Head+tail truncation for large outputs
    // Trigger LLM compaction if approaching limit
}
```

### 2.4 Multi-Agent Coordination

```
AgentManager
  ├─ LoadProfile(id) → AgentProfile
  ├─ SwitchMode(session, mode) → updates system prompt
  ├─ SpawnSession(agent, context) → new session with agent
  ├─ DelegateTask(worker, task, context) → creates worker session
  └─ JoinSession(session, agents...) → adds agents to session

Multi-agent message routing:
  - Each message has an agent_id
  - Turn-taking controlled by ChatEngine
  - Directed messages (@agent) route to specific agent
  - Free-form: all agents see all messages, respond in order
```

## 3. React Frontend Architecture

### 3.1 Component Tree

```
App
├─ AppShell (layout orchestrator)
│  ├─ NavRail (workspace switcher, search, settings)
│  ├─ LeftSidebar (Cmd+B toggle)
│  │  ├─ WorkspaceSelector
│  │  ├─ ProjectFilter
│  │  ├─ SessionList
│  │  │  ├─ PinnedSessions (drag-sortable)
│  │  │  └─ RecentSessions
│  │  └─ AgentList
│  ├─ ChatMain
│  │  ├─ ChatHeader
│  │  │  ├─ SessionTitle (editable)
│  │  │  ├─ AgentIndicator (avatar + mode badge)
│  │  │  ├─ HeaderWidgets (configurable)
│  │  │  └─ PanelToggles
│  │  ├─ ChatTranscript
│  │  │  ├─ MessageGroup (by agent)
│  │  │  │  ├─ AgentAvatar
│  │  │  │  ├─ MessageContent (markdown + envelopes)
│  │  │  │  │  ├─ ProposalCard
│  │  │  │  │  ├─ QuestionForm
│  │  │  │  │  ├─ ApprovalCard
│  │  │  │  │  └─ ToolCallIndicator
│  │  │  │  └─ MessageActions (bookmark, copy, download)
│  │  │  └─ StreamingIndicator
│  │  └─ ChatComposer
│  │     ├─ TipTapEditor
│  │     │  ├─ SlashCommandExtension
│  │     │  ├─ WikiLinkExtension
│  │     │  ├─ HashtagExtension
│  │     │  └─ FileUploadExtension
│  │     └─ ComposerToolbar
│  │        ├─ ModelPicker
│  │        ├─ ModeIndicator
│  │        └─ AttachButton
│  └─ RightRail (Cmd+/ toggle)
│     ├─ WidgetContainer
│     │  ├─ SessionInfoWidget
│     │  ├─ BookmarksWidget
│     │  ├─ ArtifactsWidget
│     │  ├─ AgentStatusWidget
│     │  ├─ ToolCallsWidget
│     │  └─ ContextBudgetWidget
│     └─ WidgetCustomizer
├─ ModalStack
│  ├─ CommandResultModal
│  ├─ AgentContentModal (rich HTML from agent)
│  ├─ WorkflowInputModal
│  └─ ArtifactPreviewModal
└─ DrawerStack
   ├─ ArtifactsDrawer
   ├─ SearchDrawer
   └─ SettingsDrawer
```

### 3.2 State Architecture

```
Zustand Stores:
  useAppStore        -- active workspace, project, session IDs
  useLayoutStore     -- panel visibility, widget config, keyboard state
  useChatStore       -- streaming state, pending approvals, active tool calls
  useAgentStore      -- loaded agent profiles, current mode

React Query:
  sessions           -- session list, CRUD, optimistic updates
  messages           -- message history, pagination
  workspaces         -- workspace/project list
  agents             -- agent profiles
  providers          -- available providers/models
  bookmarks          -- bookmarked messages

TipTap:
  Editor state managed internally by TipTap
  Slash command results from API
  File upload state local to composer
```

### 3.3 SSE Event Types

```typescript
type SSEEvent =
  | { type: 'stream_start'; message_id: string; agent_id: string }
  | { type: 'delta'; content: string }
  | { type: 'envelope'; data: Envelope }
  | { type: 'tool_call'; tool: string; args: object; status: 'started' | 'running' }
  | { type: 'tool_result'; tool: string; success: boolean; summary: string }
  | { type: 'delegation'; target_agent: string; session_id: string; task: string }
  | { type: 'approval_required'; request: ApprovalRequest }
  | { type: 'context_update'; budget_used: number; budget_total: number }
  | { type: 'stream_end'; usage: TokenUsage }
  | { type: 'error'; message: string; recoverable: boolean }
```

## 4. Database Schema

```sql
-- Workspaces (top-level context containers)
CREATE TABLE workspaces (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT,
    icon        TEXT,
    sort_order  INTEGER DEFAULT 0,
    settings    TEXT DEFAULT '{}',  -- JSON
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Projects (optional sub-grouping within workspace)
CREATE TABLE projects (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    name         TEXT NOT NULL,
    description  TEXT,
    repo_path    TEXT,           -- optional filesystem path
    settings     TEXT DEFAULT '{}',
    sort_order   INTEGER DEFAULT 0,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Agent profiles
CREATE TABLE agent_profiles (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,
    avatar          TEXT,
    system_prompt   TEXT NOT NULL,
    description     TEXT,
    modes           TEXT DEFAULT '[]',    -- JSON: available modes
    default_mode    TEXT DEFAULT 'default',
    default_model   TEXT,
    mcp_servers     TEXT DEFAULT '[]',    -- JSON: MCP server configs
    tool_permissions TEXT DEFAULT '{}',   -- JSON: allow/deny/ask per tool
    can_execute     BOOLEAN DEFAULT FALSE, -- false for Mentat, true for workers
    settings        TEXT DEFAULT '{}',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Agent modes
CREATE TABLE agent_modes (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agent_profiles(id),
    slug            TEXT NOT NULL,
    name            TEXT NOT NULL,
    prompt_addendum TEXT NOT NULL,       -- appended to system prompt
    tool_overrides  TEXT DEFAULT '{}',   -- JSON: mode-specific tool changes
    settings        TEXT DEFAULT '{}',
    UNIQUE(agent_id, slug)
);

-- Chat sessions
CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,
    short_code      TEXT NOT NULL UNIQUE,
    title           TEXT,
    custom_name     TEXT,
    workspace_id    TEXT REFERENCES workspaces(id),
    project_id      TEXT REFERENCES projects(id),
    context_type    TEXT,                -- task, sprint, entity, etc.
    context_id      TEXT,
    provider        TEXT,
    model           TEXT,
    status          TEXT DEFAULT 'active' CHECK(status IN ('active','paused','archived')),
    is_pinned       BOOLEAN DEFAULT FALSE,
    sort_order      INTEGER DEFAULT 0,
    message_count   INTEGER DEFAULT 0,
    compaction_summary TEXT,
    compacted_at    DATETIME,
    metadata        TEXT DEFAULT '{}',
    last_activity   DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Session-agent membership (multi-agent support)
CREATE TABLE session_agents (
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    agent_id    TEXT NOT NULL REFERENCES agent_profiles(id),
    mode        TEXT DEFAULT 'default',
    joined_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_primary  BOOLEAN DEFAULT FALSE,
    PRIMARY KEY (session_id, agent_id)
);

-- Messages (separate table, not JSON blob)
CREATE TABLE messages (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    agent_id    TEXT REFERENCES agent_profiles(id),  -- NULL for user messages
    role        TEXT NOT NULL CHECK(role IN ('user','assistant','system','tool')),
    content     TEXT NOT NULL,
    envelope    TEXT,              -- JSON: parsed envelope data
    metadata    TEXT DEFAULT '{}', -- JSON: provider, model, tokens, cost
    parent_id   TEXT REFERENCES messages(id), -- for threading/delegation
    is_compacted BOOLEAN DEFAULT FALSE,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Bookmarks
CREATE TABLE bookmarks (
    id          TEXT PRIMARY KEY,
    message_id  TEXT NOT NULL REFERENCES messages(id),
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    note        TEXT,
    tags        TEXT DEFAULT '[]',  -- JSON array
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Artifacts (generated files, images, documents)
CREATE TABLE artifacts (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    message_id  TEXT REFERENCES messages(id),
    name        TEXT NOT NULL,
    mime_type   TEXT NOT NULL,
    size_bytes  INTEGER,
    storage_path TEXT NOT NULL,    -- relative path in data dir
    metadata    TEXT DEFAULT '{}',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- AI providers
CREATE TABLE providers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    provider_type TEXT NOT NULL,   -- anthropic, openai, ollama, openrouter
    api_key     TEXT,              -- encrypted at rest
    base_url    TEXT,
    is_enabled  BOOLEAN DEFAULT TRUE,
    settings    TEXT DEFAULT '{}',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- AI models
CREATE TABLE models (
    id              TEXT PRIMARY KEY,
    provider_id     TEXT NOT NULL REFERENCES providers(id),
    model_id        TEXT NOT NULL,     -- e.g. claude-sonnet-4-20250514
    display_name    TEXT NOT NULL,
    context_window  INTEGER,
    max_output      INTEGER,
    supports_tools  BOOLEAN DEFAULT FALSE,
    supports_vision BOOLEAN DEFAULT FALSE,
    pricing         TEXT DEFAULT '{}', -- JSON: input/output per token
    is_enabled      BOOLEAN DEFAULT TRUE,
    sort_order      INTEGER DEFAULT 0,
    UNIQUE(provider_id, model_id)
);

-- Workflows (deterministic YAML-defined flows)
CREATE TABLE workflows (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    trigger     TEXT NOT NULL,         -- slash command trigger
    definition  TEXT NOT NULL,         -- YAML content
    workspace_id TEXT REFERENCES workspaces(id),
    is_enabled  BOOLEAN DEFAULT TRUE,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Indexes
CREATE INDEX idx_messages_session ON messages(session_id, created_at);
CREATE INDEX idx_sessions_workspace ON sessions(workspace_id, last_activity DESC);
CREATE INDEX idx_sessions_pinned ON sessions(is_pinned, sort_order);
CREATE INDEX idx_bookmarks_session ON bookmarks(session_id);
CREATE INDEX idx_artifacts_session ON artifacts(session_id);
CREATE INDEX idx_session_agents ON session_agents(session_id);
```

## 5. API Design

### 5.1 Chat Flow (Two-Phase)

```
POST /api/messages
  Body: { session_id, content, attachments? }
  Response: { message_id }
  (message persisted, response generation begins async)

GET /api/stream/:message_id
  Response: SSE stream (typed events)
  (client connects immediately after POST)
```

### 5.2 REST Endpoints

```
-- Sessions
GET    /api/sessions                    -- list (filterable by workspace/project)
POST   /api/sessions                    -- create
GET    /api/sessions/:id                -- get with recent messages
PUT    /api/sessions/:id                -- update (title, pin, model)
DELETE /api/sessions/:id                -- soft delete (archive)
POST   /api/sessions/:id/compact        -- trigger compaction

-- Messages
GET    /api/sessions/:id/messages       -- paginated history
GET    /api/stream/:message_id          -- SSE stream

-- Workspaces
GET    /api/workspaces
POST   /api/workspaces
PUT    /api/workspaces/:id
DELETE /api/workspaces/:id

-- Projects
GET    /api/workspaces/:wid/projects
POST   /api/workspaces/:wid/projects
PUT    /api/projects/:id
DELETE /api/projects/:id

-- Agents
GET    /api/agents
POST   /api/agents
PUT    /api/agents/:id
GET    /api/agents/:id/modes
POST   /api/agents/:id/modes

-- Providers
GET    /api/providers
POST   /api/providers
PUT    /api/providers/:id
POST   /api/providers/:id/test
GET    /api/models

-- Bookmarks
GET    /api/sessions/:id/bookmarks
POST   /api/bookmarks
DELETE /api/bookmarks/:id

-- Artifacts
GET    /api/sessions/:id/artifacts
GET    /api/artifacts/:id/download
POST   /api/artifacts/upload

-- Workflows
GET    /api/workflows
POST   /api/workflows/:slug/run
GET    /api/workflows/:slug/status/:run_id

-- Approvals
POST   /api/approvals/:id/approve
POST   /api/approvals/:id/reject
```

## 6. Deployment

### 6.1 Local Development
```bash
# Terminal 1: Go server with hot reload (air)
air

# Terminal 2: Vite dev server (proxied through Go)
cd ui && npm run dev
```

### 6.2 Production Build
```bash
# Build React SPA
cd ui && npm run build

# Embed in Go binary
go build -o conduit ./cmd/conduit

# Single binary serves both API and SPA
./conduit serve --port 8090
```

### 6.3 Docker (VPS)
```dockerfile
FROM golang:1.25 AS builder
# ... build steps ...
FROM gcr.io/distroless/static
COPY --from=builder /app/conduit /conduit
EXPOSE 8090
ENTRYPOINT ["/conduit", "serve"]
```

## 7. Security Considerations

- API keys stored encrypted in SQLite (at rest)
- Single-user for MVP (no auth required locally)
- VPS deployment adds basic auth or token-based auth
- MCP connections use stdio (local) or mTLS (remote)
- Tool permissions enforced per agent profile
- Approval flow for high-risk operations
