# Mentat Chat — Sprint Plan

**Date:** 2026-03-07
**Status:** Active

---

## Sprint 0: Scaffold + Foundation

**Goal:** Runnable skeleton — Go server serves React SPA, SQLite connected, 4-column layout renders.

### Tasks
- S0-01: Initialize Go module, cmd/mentat-chat entry point
- S0-02: SQLite store with embedded migrations (all tables from ARCHITECTURE.md)
- S0-03: HTTP server with router (chi or stdlib mux), static file serving
- S0-04: React project (Vite + TypeScript + Tailwind 4 + shadcn/ui) in ui/
- S0-05: 4-column layout shell (NavRail, LeftSidebar, ChatMain, RightRail)
- S0-06: Keyboard shortcuts for panel toggles (Cmd+B, Cmd+/)
- S0-07: Zustand stores (useAppStore, useLayoutStore)
- S0-08: Dev server setup (air for Go hot reload, Vite dev proxy)
- S0-09: Embed built React SPA in Go binary (go:embed)
- S0-10: Seed data: default workspace, Mentat agent profile

**Exit criteria:** `go run ./cmd/mentat-chat` serves the 4-column layout on localhost:8090 with panel toggles working.

---

## Sprint 1: Chat MVP

**Goal:** Send a message, get a streaming response from Claude, see it rendered with markdown.

### Tasks
- S1-01: Workspace + project CRUD API endpoints
- S1-02: Session CRUD API endpoints (create, list, get, update, archive)
- S1-03: Message send endpoint (POST /api/messages → persist + return ID)
- S1-04: Anthropic streaming adapter (Go, SSE to client)
- S1-05: SSE stream endpoint (GET /api/stream/:id → typed events)
- S1-06: TipTap composer component (Enter to send, Shift+Enter newline)
- S1-07: Chat transcript component (message list, auto-scroll, streaming indicator)
- S1-08: Markdown rendering (react-markdown + remark-gfm + syntax highlighting)
- S1-09: Session list in left sidebar (recent sessions, click to switch)
- S1-10: Session title (auto from first message, editable)
- S1-11: Workspace selector in nav rail
- S1-12: Basic system prompt assembly (agent profile + workspace context)

**Exit criteria:** User can create sessions, send messages, receive streaming Claude responses with markdown rendering. Sessions persist across page reloads.

---

## Sprint 2: Agent + Mode System

**Goal:** Agent profiles with modes, mode switching, multi-agent session foundation.

### Tasks
- S2-01: Agent profile CRUD API + management UI
- S2-02: Mode definitions (DB-stored, prompt addendum)
- S2-03: `/mode` slash command — switch mode within session
- S2-04: Mode indicator in composer toolbar + chat header
- S2-05: System prompt assembly with mode addendum
- S2-06: Multi-agent session support (session_agents table, agent attribution on messages)
- S2-07: Agent avatars + name display in transcript (multi-agent differentiation)
- S2-08: Directed messages (@agent) routing
- S2-09: Session spawning API (agent creates a new session programmatically)
- S2-10: Provider/model CRUD API + model picker in composer toolbar

**Exit criteria:** Mentat can be in different modes. Sessions show agent attribution. Model picker works. Foundation for multi-agent is in place.

---

## Sprint 3: MCP + Tools

**Goal:** Connect to Tiamat MCP servers. Worker agents execute tool calls. Mentat delegates.

### Tasks
- S3-01: MCP client in Go (stdio transport for local MCP servers)
- S3-02: MCP tool discovery — auto-register tools from connected servers
- S3-03: Tool call routing — agent tool_use → MCP server → result
- S3-04: Tool call UI — progress indicators, result display in transcript
- S3-05: Envelope v2 parser — detect and parse structured blocks in responses
- S3-06: Proposal cards — editable inline forms with apply/dismiss
- S3-07: Question forms — structured input collection (radio, select, textarea)
- S3-08: Approval cards — inline approve/reject with risk display
- S3-09: Delegation flow — Mentat creates delegation envelope → worker session spawned
- S3-10: Worker session monitoring — status updates back to originating session

**Exit criteria:** Mentat can delegate tasks to worker agents who execute via MCP tools. Volon/Cortex/Hadron tools are available. Envelopes render as interactive cards.

---

## Sprint 4: Context + UX Polish

**Goal:** Smart context management, bookmarks, widgets, keyboard-first UX.

### Tasks
- S4-01: Context broker — workspace + project + session history assembly
- S4-02: Budget enforcement — 75% ceiling, per-turn pruning
- S4-03: Session compaction (LLM-based summarization)
- S4-04: Context budget widget (visual indicator in right rail)
- S4-05: Bookmarks — toggle per message, bookmark widget in right rail
- S4-06: Widget system — configurable widgets for both rails + header
- S4-07: Slash commands via TipTap autocomplete
- S4-08: Artifacts drawer — slide-out panel for generated files
- S4-09: Session pinning + drag-sort in sidebar
- S4-10: Command result modal with navigation stack
- S4-11: Full keyboard shortcut system
- S4-12: Agent-generated modals (rich HTML/dashboard content)

**Exit criteria:** Context is managed automatically. Bookmarks work. Widget system is configurable. All panels have keyboard shortcuts. Artifacts are downloadable.

---

## Sprint 5: Production + Deployment

**Goal:** Multi-provider support, workflows, Docker deployment, VPS-ready.

### Tasks
- S5-01: OpenAI streaming adapter
- S5-02: Ollama streaming adapter (local models)
- S5-03: Deterministic workflow engine (YAML parser + executor)
- S5-04: Workflow input modals (dynamic forms from YAML definition)
- S5-05: Docker build (multi-stage, distroless base)
- S5-06: VPS deployment config (docker-compose with Tiamat services)
- S5-07: Basic auth for VPS deployment
- S5-08: MCP HTTP transport (for remote MCP servers)
- S5-09: Agent-to-agent direct communication (no user in loop)
- S5-10: Multi-agent group sessions (2+ agents in same chat)

**Exit criteria:** App runs in Docker on VPS. Multiple AI providers available. Workflows can be defined and triggered. Multi-agent collaboration is fully functional.
