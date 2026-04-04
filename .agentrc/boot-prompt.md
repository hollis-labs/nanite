# Session Boot Prompt

## Backend Agent

```
Boot nanite-backend

Rebrand from Conduit → Nanite complete (all 5 waves, 2026-04-03).
vNext MVP Phases 0-7 complete. Post-MVP Phases A+B complete.
Cerberus service live: nanite-api (port 8090), nanite-frontend (port 5176).

KEY DOCS:
- Brand package: internal/brand/brand.go (single source of truth for app identity)
- MVP plan: docs/vnext-mvp.md (8 phases, all complete)
- Post-MVP backlog: docs/vnext-backlog.md
- Plugin extraction plan: docs/plugin-extraction-plan.md (6 phases)

ARCHITECTURE:
- Migrations: single 001_schema.sql (DDL only). All seed data in seed.go.
- Agents/skills: file-based (MD + YAML frontmatter), DB stores runtime state only
- Plugins: YAML manifest + Go/subprocess, event hooks, connectors, UI components
- Providers: 8 HTTP API + 8 CLI adapters via PTY bridge

COMPLETED (pre-rebrand):
- Service layer decomposition: internal/service/ with Container
- Phase 0-4: tool system, context window, permissions, chat loop hardening
- Plugin system: SDK, discovery, events, config, connectors, scaffold, generator

COMPLETED: Phases 5-7 — Agent System, Skill System, Slash Commands & Polish
- File-based agents + skills with builtin defaults
- Slash commands auto-registered from skills
- Migration auto-discovery via fs.ReadDir()
- Envelope sync test, nil check consistency, real tool token costs
- 18 tests (commands + skill service), full suite green

COMPLETED: Post-MVP Phases A+B
- A1: MCP Config Import/Export (CLI + GUI)
- A2: Token Breakdown (tool_input_tokens, per-block parsing)
- B1: Plugin/Event Enhancements (hook aliases, loadType system, preferences)
- B2: Multi-Agent Orchestration (Badger KV, task tracking, workers, worktrees)

COMPLETED: Migration Squash (2026-04-04)
- 27 migrations → single 001_schema.sql (DDL only)
- Seed data consolidated in seed.go (providers, models, user_settings, catalog)
- Stale conduit-plugins catalog URL fixed → nanite-plugins
- CI enforcement: grep for INSERT in .sql files (planned)

CURRENT: Plugin Extraction (docs/plugin-extraction-plan.md)
- Phase 1 (cleanup): DONE
- Phase 2 (Fragments Engine): DONE
- Phase 3 (debug widgets): DONE — builtin debugwidgets plugin, frontend in plugins/debug/
- Phase 4 (TaskBackend): DONE — TaskBackend interface, LocalBackend, registry, Host.RegisterTaskBackend, migration 002
- Phase 5: Connector plugins (linear, slack, github, email)
- Phase 6: Remaining extractions (email/teams/documents envelopes, bookmarks, actions)

CURRENT: Phase C — Memory & Continuity
- Check Cortex for type/view registry changes needed
- MemoryService, extraction (PostCompact + per-turn), memory tools (opt-out)

UPCOMING: User Shell (core feature, not plugin)
Two tasks, build in order:

  Task 1: ! Shell Exec
  - Parse `!` prefix in chat composer (frontend) and internal/chat/commands.go (backend)
  - Convention matches Claude Code's `!` prefix — standard, not custom
  - Backend: execute command via os/exec, capture stdout+stderr, inject as user message visible to LLM
  - Agent sees command + output, can comment on errors, suggest corrections
  - Working directory: project root (from session/config), fallback $HOME
  - Output truncation via internal/truncate/ pipeline
  - Default denylist (internal/shell/denylist.go): destructive commands blocked unless YOLO
  - YOLO lightning icon becomes 3-state toggle: Ask (confirm each) → Session (auto-approve, denylist active) → YOLO (no restrictions)
  - Frontend: `!` keystroke triggers info drawer (2-line slide-up from composer top):
    [lock-icon] ~/Projects-apps/nanite  ·  main  ·  clean
    Lock icon toggles denylist on/off per session. Admin user setting controls default.
  - Drawer disappears on submit or backspace out of `!` mode

  Task 2: Interactive Shell Tab (PTY) — depends on Task 1
  - Shell icon in composer toolbar (between YOLO toggle and paperclip), keyboard shortcut TBD
  - Backend: PTY session manager — spawn user's default shell, scoped to chat session lifetime
  - Transport: WebSocket (new endpoint, SSE insufficient for bidirectional)
  - Frontend: xterm.js terminal embed, replaces composer area when toggled
  - Output is NOT in LLM context by default — user can select+send snippets to conversation
  - Requires shell_mode enabled (dev mode)
  - Shell process killed on session archive
  - Same info drawer chrome (path + git + denylist toggle) applies

PHASE D (future): Claude Code Integration

PRINCIPLES:
- Consult before architecture decisions
- Greenfield modules alongside old code, migrate when ready
- go build + go vet + go test clean after every task
- Always use cerberus_rebuild for deployment, not go build directly
- Quality over speed. Polished software, maintainable patterns.
- Brand package: use brand.* constants, never hardcode app name/identity
- Migrations = DDL only. seed.go = data only.
- Connectors are standalone plugins, feature plugins consume them.

Test suite: all passing (zero failures)
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

COMPLETED (vNext frontend, 2026-04-03):
- P0-P3 debug panels, approval cards, broker inspector, compaction divider
- Tool call drawer with session-scoped retention
- All 6 anti-patterns resolved
- 23 shadcn components installed

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

Future (plugin extraction related):
- Debug widgets will move to plugin-debug (Phase 3 of extraction plan)
- Engine/sprint UI will move to plugin-fragments-engine (Phase 2)
- See docs/plugin-extraction-plan.md for full details
```
