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
- Agent adapters: CLIAgentAdapter interface in internal/agent/adapter.go.
  5 built-in adapters: nanite-native, claude, codex, gemini, opencode.
  AdapterRegistry manages discovery + sandbox population + project root sync.
  Config override cascade: Agent Base → Project → Session (per-field merge).
- Plugins: YAML manifest + Go/subprocess, event hooks, connectors, UI components.
- Providers: shared lib at hollis-labs/go-providers (8 HTTP API + 8 CLI adapters).
- Service layer: internal/service/container.go wires all services.
- engine.go: thin orchestrator (197 lines), delegates to service layer.
- Sandboxing: AgentExec (full isolation) / UserExec (guardrails+sandbox). macOS seatbelt + Linux bwrap.
- Network proxy: domain-allowlisted localhost TCP proxy, injected via HTTP_PROXY.
- Sandbox-first: OS sandbox is primary boundary, denylist is second line. YOLO = no sandbox, denylist stays.
- Sandbox content: delegated to adapter plugins via PopulateAllSandboxes().
- Envelope contracts: config/envelopes.yaml manifest → Go init + TS codegen (A+ validated sync).
- Workflow engine: internal/workflow/ (DAG executor, 5 step handlers, YAML loader).
- Todo/Plan system: internal/store/todos.go + plans.go, 3 scopes, 13 API routes, 5 agent tools.
- Memory: internal/memory/ (embedded Conduit Go lib, per-turn + post-compact extraction).
- Context broker: 5 sources wired (conduit 25%, memory 15%, pcc 30%, engine 15%, session 15%).
- Embeddings: OpenAI text-embedding-3-large (preferred) or Ollama nomic-embed-text (fallback).
- Filter chain: reasoning-blind view support (FilterViewReasoningBlind).
- Messaging: SQLite-native A2A (internal/store/a2a.go). Nexus removed.

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
  ✅ Version centralized (internal/version/version.go)
  ✅ MCP server paths DB-loaded (no more hardcoded paths/tokens)
  ✅ Nil check pattern standardized (reads→empty, writes→error)
  ✅ Auth middleware test fixed (401 on missing/wrong creds)

MEMORY:
  ✅ Similarity ranking enabled (source_memory.go, 2026-04-08).
  Uses Ollama nomic-embed-text by default; prefers OpenAI text-embedding-3-large when OPENAI_API_KEY is set.

TECH DEBT (resolved 2026-04-08):
  ✅ Hardcoded default model — uses a.Services.UtilityModel now
  ✅ Anonymous struct request bodies — 51 named types in internal/api/types.go
  ✅ Fat cmdServe() — extracted 4 init functions, down to ~215 lines
  ✅ Envelope sync fragility — A+ approach: config/envelopes.yaml manifest + JSON Schema
    Single source of truth, Go reads at startup, TS codegen generates both sides.
    CI: `node scripts/generate-plugin-imports.mjs --check`

AGENT ADAPTER ARCHITECTURE — COMPLETED (PR #11, 2026-04-08):
  ✅ CLIAgentAdapter + AgentComposer interfaces (internal/agent/adapter.go)
  ✅ AdapterRegistry (priority-sorted, copy-before-iterate)
  ✅ 5 adapter plugins: nanite-native, claude, codex, gemini, opencode
  ✅ Config override cascade (Agent Base → Project → Session, per-field merge)
  ✅ session_agent_overrides table
  ✅ Managed section protocol (<!-- nanite:start --> blocks)
  ✅ Nexus dependency removed, SQLite-only messaging
  ✅ .agentrc → .nanite rename in Go code (nanite-native retains fallback)
  ✅ Sandbox delegation to adapters
  Spec: docs/superpowers/specs/2026-04-08-agent-adapter-architecture-design.md

BETA KNOWN ISSUES SWEEP — COMPLETED (2026-04-10):
  ✅ P0 #1 TestShutdown data race in internal/worker — merged 1343446 (Worker.Status RWMutex + StatusCancelled guard)
  ✅ P0 #2 TestTriggerDispatch race in internal/plugin — merged 48bf67e (testConnector stub mutex)
  ✅ P0 #3 Worker.SessionID/WorktreePath concurrent access — merged d5da5bb (RWMutex extended, Snapshot type, Manager.List/ReapStale return []Snapshot)
  ✅ P0 #4 Dev-mode binary mismatch — closed by documentation (.nanite/agents/backend.md §Build & Run "Two binaries, only one is live")
  Repo-wide go test -race ./... clean post-merge. docs/beta-known-issues.md has zero open P0/P1 items.

PLUGIN ENVELOPE EMISSION — INVESTIGATION DEFERRED (2026-04-10):
  Discovered during plugin extraction attempt that the plugin envelope emission story is broken system-wide, not just
  for fragments-engine and support-ticket. Documented in docs/architecture/plugin-envelope-emission-findings-2026-04-10.md.
  Key facts (see doc for file:line pointers and runtime evidence):
  - Only ONE working emission path: tool handler returns <!--ENVELOPE_DATA:{json}:ENVELOPE_DATA--> markers → captureEnvelopeData in chat_generate.go
  - ValidateEnvelope is ADVISORY, not blocking — unregistered_type errors are logged but the envelope passes through
  - event.Data["envelope"] path in giphy/oembed builtin plugins is DEAD CODE (no readers anywhere)
  - Nothing in the backend reads plugin.yaml registers.envelopes — only consumed by frontend TypeScript codegen
  - plugins/fragments-engine and plugins/support-ticket use internal/* imports (internal/chat, internal/mcp, internal/store) that an external Go module cannot use
  - Core nanite self-tools and chat_generate.go emit "plugin" envelope types directly (kb-result, ticket-confirmation, task-disposition, task-complete-notification, giphy-modal)
  8 gaps enumerated in the findings doc. NEXT STEP: plugin agent boots cold from that doc, owns the extraction work. Not a backend-agent concern going forward.

UPCOMING:
- Future adapters: CrewAI (first), AutoGen, LangGraph
- Plugin Extraction Phase 5 (Connectors) — PARKED, pending first connector.
  Infrastructure complete: Connector interface, Host.RegisterConnector, health tracking,
  trigger dispatcher w/ retry, API endpoints, tests. No concrete connectors yet.
- User Shell Task 2 (Interactive PTY) — backlogged (! exec sufficient)
- Phase D (Claude Code Integration) — future
- Frontend: Agent Adapter UI (source badges, override editor, sync status, NANITE.md preview)
- Slash commands: additional commands as needed (Host.RegisterCommand ✅)
- .agentrc/ → .nanite/ file rename (separate agent handles agentrc repo changes)

PRINCIPLES:
- Consult before architecture decisions.
- Greenfield modules alongside old code, migrate when ready.
- go build + go vet + go test clean after every task.
- Always cerberus_rebuild nanite-api for deployment — never raw go build.
- Brand package: use brand.* constants, never hardcode identity.
- Migrations = DDL only. seed.go = data only.
- Envelope sync: config/envelopes.yaml is source of truth → Go init + TS codegen + --check CI.
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
- .nanite/agents/frontend.md — full project context
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

COMPLETED FRONTEND TASKS (since vNext):
  ✅ Tool Load Preferences UI
  ✅ Worker Status UI
  ✅ Envelope type codegen (npm run generate:envelopes / check:envelopes)
  ✅ Workflow progress panel
  ✅ Memory viewer

COMPLETED (Beta Polish — PR #18, 2026-04-10):
  ✅ Tooltip.tsx → tooltip.tsx rename — fixes fresh-worktree/clone build failures
     under forceConsistentCasingInFileNames (git had tracked capitalized filename
     since Sprint 0; main had an uncommitted on-disk rename APFS-hid from git)
  ✅ Plugin slot lookup simplification — replaced gitignored empty-scaffolding
     ui/src/generated/plugin-slot-components.ts with 4-line runtime shim at
     ui/src/lib/plugin-slot-lookup.ts; fresh checkouts now build
  ✅ Search → jump-to-message — SearchModal clicks navigate to target session,
     scroll target message into view, flash 1.5s ring highlight. Works for both
     same-session and cross-session jumps via pendingJump + scrollToMessageId
     state bus in useChatStore and a dedicated reactive effect in useChat.
  ✅ Anti-pattern checklist cleanup — items 1, 2, 3, 5, 6 in frontend.md
     §Anti-Patterns Found were closed in prior work but the list wasn't
     updated; now reflects reality. Dead `listAgentProfiles` alias removed.
  ✅ New §Known Gaps section in .nanite/agents/frontend.md documenting:
     (1) plugin slot component resolution is incomplete — backend slot system
     is live but frontend component resolution was never built. No plugin
     registers a React component via registerSlotComponent() at runtime;
     real plugins bypass the slot system with hardcoded AppShell mappings.
     See docs/architecture/plugin-system.md §13.
     (2) chat window-mode pagination — after a jump-to-message, useChat
     fetches a centered window but paginationState is set to null because
     the messages?around= endpoint doesn't return oldest_offset, so
     "load older" is hidden until session re-entry. Post-beta fix: extend
     backend response.

PENDING FRONTEND TASKS:

1. Agent Adapter UI (priority: medium, from PR #11)
   - Adapter source badges — show source (nanite, claude, codex, etc.) on agent list
   - Override editor — project + session overrides, cascade visualization
   - Sync status — active adapters, last sync, errors
   - NANITE.md preview — managed section content with "sync now" button
   - Adapter management — enable/disable per adapter
   - Backend: AdapterRegistry, 5 adapters, override cascade all ready
   - Spec: docs/superpowers/specs/2026-04-08-agent-adapter-architecture-design.md § 8

2. Window-mode pagination for jump-to-message (post-beta)
   - Extend backend `/api/sessions/{id}/messages?around={msg}` to return
     oldest_offset for the window so useChat can build a real paginationState
   - Re-enable "load older" button after jump lands
   - See frontend.md §Known Gaps for full context
```
