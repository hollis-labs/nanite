# Session Boot Prompt

## Backend Agent

```
Boot nanite-backend

Rebrand Conduit → Nanite complete. vNext MVP 0-7 ✅, Post-MVP A+B ✅.
Plugin Extraction Phases 1-4 ✅, User Shell Task 1 (! exec) ✅, Phase C investigation ✅.
Cerberus services: nanite-api (8090), nanite-frontend (5176).

KEY DOCS:
- Brand: internal/brand/brand.go (single source of truth)
- Plugin extraction: docs/plugin-extraction-plan.md
- Hooks/events/filters: docs/plugin-hooks-events-filters.md
- Phase C memory: docs/phase-c-cortex-investigation.md
- Backlog: docs/vnext-backlog.md
- Post-MVP: docs/post-mvp-plan.md

ARCHITECTURE:
- Migrations: single 001_schema.sql (DDL only). Seed data in seed.go.
- Agents/skills: file-based (MD + YAML frontmatter), DB = runtime state only.
- Plugins: YAML manifest + Go/subprocess, event hooks, connectors, UI components.
- Providers: 8 HTTP API + 8 CLI adapters via PTY bridge.
- TaskBackend: pluggable (LocalBackend default), registered via Host.RegisterTaskBackend.

RECENTLY COMPLETED:
- Plugin Hooks, Events & Filters (PR #6, 2026-04-05) — all 4 tasks:
  - Task 1: Pre-hooks (message.sending, tool.executing with cancellation),
    11 new emitters, 10+ orphan emitters wired, new event constants.
  - Task 2: FilterRegistry with priority-ordered synchronous chains,
    6 filter points wired (system_prompt, user_message, tool_result,
    assistant_response, context_window, envelope_data).
  - Task 3: 6 new UI slots (composer-above/below, message-actions/header,
    session-sidebar, modal), usePluginAction hook, global PluginModal.
  - Task 4: context-menu:message/session (right-click), command-palette (Cmd+K).
  - UnloadPlugin now cleans up filters, UI components, keybindings.
- User Shell Task 1 — `!` exec with denylist, 3-mode approval. PRs #4, #5.
- Theme editor + color system redesign (brand/primary/danger split, live preview).
- Plugin extraction Phases 1-4 (cleanup, Fragments Engine, debug widgets, TaskBackend).
- Phase C Cortex investigation — gap analysis, namespace strategy, 13 open questions.
- Plugin Extraction Phase 6 — Bookmarks extracted as builtin plugin.
  Eliminated: email, teams, documents, actions, demo-presenter (removed or core).
  bookmark events: message.bookmarked / message.unbookmarked wired.
- Plugin Extraction Phase 7 — 8 envelope primitives built:
  info-card, list-card, metric-card, progress-card, confirmation-card,
  table-card, timeline-card, diff-card. All in ui/src/.../primitives/.

CURRENT: Plugin Extraction Phase 5 — Connectors (GitHub only)
- Scope narrowed: linear/slack/email dropped, github only.
- Transport priority pattern: CLI > MCP > API with auto-detection.

UPCOMING (ordered):

1. User Shell Task 2 — Interactive PTY Shell Tab
   - Shell icon in composer toolbar; WebSocket endpoint (SSE insufficient).
   - Backend PTY session manager scoped to chat session lifetime.
   - Frontend xterm.js embed replacing composer area when toggled.
   - Output NOT in LLM context by default — user selects snippets to send.
   - Same info drawer chrome (path + git + denylist toggle).
   - Shell process killed on session archive. Requires shell_mode enabled.

2. Phase C — Memory & Continuity (implementation)
   - Resolve 13 open questions from investigation.
   - Likely blocked on Cortex: needs memory type + memory_recall view + embedding provider.
   - Then: MemoryService, extraction (PostCompact + per-turn), memory tools (opt-out).

3. Phase D — Claude Code Integration (future, lower priority)

PRINCIPLES:
- Consult before architecture decisions.
- Greenfield modules alongside old code, migrate when ready.
- go build + go vet + go test clean after every task.
- Always cerberus_rebuild nanite-api for deployment — never raw go build.
- Brand package: use brand.* constants, never hardcode identity.
- Migrations = DDL only. seed.go = data only.
- Envelope sync: backend envelope.go ↔ frontend plugin-envelopes.ts (manual, silent drop).
- Quality over speed.

Test suite: all passing.
```

## Frontend Agent

```
Boot nanite-frontend

Rebrand from Conduit → Nanite ��� all waves complete.

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- .agentrc/agents/frontend.md — full project context
- CLAUDE.md — envelope system warnings

ARCHITECTURE:
- Backend fully rebranded: module github.com/hollis-labs/nanite
- Envelope protocol: nanite-envelope
- Tool names: nanite_*
- Env vars: NANITE_*
- Brand package at internal/brand/brand.go — frontend mirrors with ui/src/brand.ts

COMPLETED (vNext frontend):
- P0-P3 debug panels, approval cards, broker inspector, compaction divider
- Tool call drawer with session-scoped retention
- All 6 anti-patterns resolved, 23 shadcn components installed
- Plugin Hooks/Events/Filters Tasks 3+4 (PR #6, 2026-04-05):
  - 6 new UI slots wired: composer-above/below, message-actions/header,
    session-sidebar, modal — all using usePluginSlots()
  - usePluginAction hook + global PluginModal in AppShell
  - context-menu:message/session right-click menus with plugin items
  - command-palette (Cmd+K) renders plugin-registered commands

PENDING FRONTEND TASKS:

1. Tool Load Preferences UI (priority: high)
   - Backend APIs ready: GET/PUT /api/tools/load-preferences, GET /api/tools/all
   - Settings panel where users toggle tool load types (auto/opt-in/disabled) per tool
   - Follow patterns in ToolsWidget.tsx

2. Task Tracking UI (priority: high)
   - Backend APIs ready: GET/POST /api/tasks, GET/PUT/DELETE /api/tasks/{id}
   - POST /api/sessions/{id}/tasks/{id}/transition
   - Tasks have status (pending/in_progress/completed/failed), belong to sessions
   - Need: task list widget, create/edit form, status transition buttons
   - Existing envelopes: TaskDispositionCard.tsx, TaskCompleteNotificationCard.tsx

3. Worker Status UI (priority: medium)
   - Backend APIs ready: GET /api/workers, POST /api/workers/{id}/cancel
   - Workers are background multi-agent orchestration processes
   - Need: worker list widget showing active workers, status, cancel button

4. Volon Backlog Button Polish (priority: low)
   - VolonBacklogButton.tsx exists, verify it works with POST /api/volon/backlog
```
