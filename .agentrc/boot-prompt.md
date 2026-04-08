# Session Boot Prompt

## Backend Agent

```
Boot nanite-backend

Rebrand Conduit → Nanite complete. vNext MVP 0-7 ✅, Post-MVP A+B ✅.
Plugin Extraction Phases 1-4, 6, 7 ✅. User Shell Task 1 (! exec) ✅.
Hardening Phase ✅ (2026-04-07). Provider lib extracted to hollis-labs/go-providers.
Sandbox Hardening ✅ (2026-04-07): Linux bwrap, network proxy, sandbox-first model.
Memory System ✅ (2026-04-07): Conduit embedded as Go lib, context broker wired, embeddings live.
Cerberus services: nanite-api (8090), nanite-frontend (5176).

KEY DOCS:
- Brand: internal/brand/brand.go (single source of truth)
- Hardening plan: docs/hardening-phase-plan.md
- Plugin extraction: docs/plugin-extraction-plan.md
- Hooks/events/filters: docs/plugin-hooks-events-filters.md
- Phase C memory: docs/phase-c-cortex-investigation.md
- Research: docs/research/ (alignment matrix, gaps & opportunities)

ARCHITECTURE:
- Migrations: DDL only. Seed data in seed.go.
- Agents/skills: file-based (MD + YAML frontmatter), DB = runtime state only.
- Plugins: YAML manifest + Go/subprocess, event hooks, connectors, UI components.
- Providers: shared lib at hollis-labs/go-providers (8 HTTP API + 8 CLI adapters).
- Service layer: internal/service/container.go wires all services.
- engine.go: thin orchestrator (197 lines), delegates to service layer.
- Sandboxing: AgentExec (full isolation) / UserExec (guardrails+sandbox). macOS seatbelt + Linux bwrap.
- Network proxy: domain-allowlisted localhost TCP proxy, injected via HTTP_PROXY.
- Sandbox-first: OS sandbox is primary boundary, denylist is second line. YOLO = no sandbox, denylist stays.
- Envelope contracts: JSON schemas → Go test + TS codegen (automated sync).
- Workflow engine: internal/workflow/ (DAG executor, 5 step handlers, YAML loader).
- Todo/Plan system: internal/store/todos.go + plans.go, 3 scopes, 13 API routes, 5 agent tools.
- Memory: internal/memory/ (embedded Conduit Go lib, per-turn + post-compact extraction).
- Context broker: 5 sources wired (conduit 25%, memory 15%, pcc 30%, engine 15%, session 15%).
- Embeddings: OpenAI text-embedding-3-large (preferred) or Ollama nomic-embed-text (fallback).
- Filter chain: reasoning-blind view support (FilterViewReasoningBlind).

HARDENING PHASE — COMPLETED (2026-04-07):
  ✅ Task 1 — God-object decomp (engine.go 2164→197 lines)
  ✅ Task 2 — Sandboxing Tier 1+2 (AgentExec/UserExec, macOS seatbelt)
  ✅ Task 3 — Envelope contracts (24 schemas, Go tests, TS codegen)
  ✅ Task 4 — Memory system (Conduit-backed, extraction hooks, agent tools)
  ✅ Task 5 — Todo/plan system (3 scopes, plans, 13 API routes, 5 agent tools)
  ✅ Task 6 — Workflow engine v2 (DAG executor, 5 handlers, YAML loader)
  ✅ Task 7 — Dead event audit (2 deleted, 1 wired, 6 planned-v2)
  ✅ Task 8 — Progressive threshold (5→10, single source)
  ✅ Task 9 — Code execution (nanite_code_execute, shell/python/js)
  ✅ Think tool, Reasoning-blind filter view
  ✅ Provider lib extraction (hollis-labs/go-providers)

REMAINING:
- Memory: similarity ranking available when OPENAI_API_KEY set or Ollama running.
  Activation ranking is the default in MemorySource. Switch to similarity
  ranking in contextbroker/source_memory.go when ready to test.

UPCOMING:
- Plugin Extraction Phase 5 (Connectors) — paused for hardening, ready to resume
- User Shell Task 2 (Interactive PTY) — backlogged (! exec sufficient)
- Phase D (Claude Code Integration) — future
- Frontend: workflow progress panel, memory viewer
- Slash commands: /status, /providers, plugin-registered commands
- Agent model alignment (agentrc schema extensions)

PRINCIPLES:
- Consult before architecture decisions.
- Greenfield modules alongside old code, migrate when ready.
- go build + go vet + go test clean after every task.
- Always cerberus_rebuild nanite-api for deployment — never raw go build.
- Brand package: use brand.* constants, never hardcode identity.
- Migrations = DDL only. seed.go = data only.
- Envelope sync: JSON schemas are source of truth → Go test + TS codegen.
- Shell exec trust: AgentExec = full isolation, UserExec = guardrails only.
- Quality over speed.

Test suite: all passing.
```

## Frontend Agent

```
Boot nanite-frontend

Rebrand from Conduit → Nanite — all waves complete.

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- .agentrc/agents/frontend.md — full project context
- CLAUDE.md — envelope system warnings

ARCHITECTURE:
- Backend fully rebranded: module github.com/hollis-labs/nanite
- Envelope protocol: nanite-envelope
- Envelope types: TS codegen from JSON schemas (ui/src/generated/envelope-types.generated.ts)
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

COMPLETED (Todo/Plan UI — PR #9, 2026-04-08):
  - "Work" tab replaces old "Tasks" tab in RightRail
  - Collapsible Todos + Plans sections with scope switcher (Session/Project)
  - Drag-and-drop reorder (@dnd-kit), checkbox toggle, undo-with-feedback
  - "Send Changes" button with dirty tracking, auto-inject on next message
  - Toast notification in composer chrome (auto-dismiss)
  - Envelope cards: todo-list, plan-review (approve/edit steps/reject)
  - Backend: POST /api/work/sync, SSE reactivity for real-time updates
  - Old session-task code fully removed (SessionTasksTab, useSessionTasks, etc.)
  - Spec: docs/superpowers/specs/2026-04-08-todo-plan-ui-design.md

PENDING FRONTEND TASKS:

1. Tool Load Preferences UI (priority: high)
   - Backend APIs ready: GET/PUT /api/tools/load-preferences, GET /api/tools/all
   - Settings panel where users toggle tool load types (auto/opt-in/disabled) per tool
   - Follow patterns in ToolsWidget.tsx

2. Worker Status UI (priority: medium)
   - Backend APIs ready: GET /api/workers, POST /api/workers/{id}/cancel
   - Workers are background multi-agent orchestration processes
   - Need: worker list widget showing active workers, status, cancel button

3. Envelope types (priority: medium, automated)
   - TS types auto-generated from JSON schemas via scripts/generate-envelope-types.mjs
   - Run `npm run generate:envelopes` or `make generate-envelopes`
   - Staleness check: `npm run check:envelopes`

4. Workflow progress panel (priority: low, future)
   - Backend workflow engine ready (internal/workflow/)
   - Show pipeline step status, progress, events
   - Reserved: collapsible section in WorkTab (hidden when empty)

5. Memory viewer (priority: low, future)
   - Show recalled memories in context, extraction history
   - Reserved: future RightRail tab or widget
```
