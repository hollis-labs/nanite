# Session Boot Prompt

## Backend Agent

```
Boot nanite-backend

Rebrand from Conduit → Nanite complete (all 5 waves, 2026-04-03).
vNext MVP Phases 0-4 complete prior to rebrand.
Cerberus service live: nanite-api (port 8090), nanite-frontend (port 5176).

KEY DOCS:
- Rebrand plan: docs/rebrand-plan.md (all 5 waves complete)
- Brand package: internal/brand/brand.go (single source of truth for app identity)
- MVP plan: docs/vnext-mvp.md (8 phases, selected work)
- Post-MVP backlog: docs/vnext-backlog.md

REBRAND STATUS:
- Wave 0: Old Nanite → Nil, plugin contract → ~/Projects-apps/plugin/, new repo cloned
- Wave 1: Brand package, Go module (github.com/hollis-labs/nanite), 107 files rewritten, cmd/nanite/
- Wave 2: Env vars (NANITE_*), config/nanite.yaml, Makefile, Docker, Cerberus (nanite-api)
- Wave 3: Frontend UI strings — DONE
- Wave 4: Docs/ADRs/agentrc — DONE

COMPLETED (pre-rebrand):
- Service layer decomposition (Waves 0-4): internal/service/ with Container
- Phase 0-4 of vNext: tool system, context window, permissions, chat loop hardening
- Plugin system: SDK, discovery, events, config, connectors, scaffold, generator

COMPLETED: Phase 5 — Agent System (2026-04-03)
- internal/agent/ package: parser, discovery (6 locations), convert, builtin default
- Agents now file-based (MD + YAML frontmatter), DB stores runtime state only
- Seed.go: removed all agent seeding + SeedAgentSkillBindings
- AgentService: file-based agents primary, DB fallback, fallback → file-default
- Worktree isolation deferred

COMPLETED: Phase 6 — Skill System (2026-04-03)
- internal/skill/ package: parser, discovery (5 locations), convert, dynamic context
- Skills now file-based (MD + YAML frontmatter), DB stores runtime state only
- 8 built-in skills as embedded .md files (internal/skill/builtin/)
- SkillService: file-based primary, DB fallback, source filtering
- Slash commands: skills auto-registered as /slug [args]
- SeedBuiltinSkills() removed from main.go
- Frontend: source filter pills + SourceBadge (matches agent UI pattern)
- Runtime execution wiring (inline/fork) deferred to Phase 7

COMPLETED: Phase 7 — Slash Commands & Polish (2026-04-04)
- /mode command added (client-side), /status /providers /export /search already existed
- Migration ordering: auto-discovery via fs.ReadDir() (no more manual file list)
- Envelope sync: TestEnvelopeRegistrySync validates backend↔frontend alignment
- Nil check consistency: list endpoints return empty, action endpoints return errors
- Real tool token costs from execution_metrics, slot inspector debug endpoint
- Snapshot field alignment: site, duration_ms, max_turns, parallel
- Fixed TestAuthMiddlewareEnabled (rebrand env var leftover)
- 18 new tests (commands + skill service), go test zero failures

MVP PHASES 0-7 COMPLETE.

POST-MVP PLAN (decided 2026-04-04):
Phase A (parallel): MCP Import/Export + Token Breakdown — DONE
Phase B (parallel): Plugin/Event Enhancements + Multi-Agent Orchestration — DONE
Phase C (after B1): Memory & Continuity
Phase D (after B1): Claude Code Integration
See docs/post-mvp-plan.md for full details and decisions.

COMPLETED: Phase A1 — MCP Config Import/Export (2026-04-04)
- nanite mcp import/export CLI commands in cmd/nanite/main.go
- internal/mcpconfig/ package: ParseMCPJSON, ExportMCPJSON
- GUI import via POST /api/mcp-servers/import

COMPLETED: Phase A2 — Token Breakdown (2026-04-04)
- tool_input_tokens column (migration 025), per-block parsing from Anthropic responses
- Updated RecordUsage, SessionUsageSummary, TokenUsageWidget frontend

COMPLETED: Phase B1 — Plugin/Event Enhancements (2026-04-04)
- Claude Code hook aliases (NormalizeEventType), loadType system (auto/opt-in)
- LoadTypeResolver: session > project > agent > manifest override chain
- User tool load preferences (migration 026), API endpoints, MCP filtering

COMPLETED: Phase B2 — Multi-Agent Orchestration (2026-04-04)
- Badger KV coordination store: internal/coordination/ (store, keys, badger, noop)
- Task tracking: internal/task/ (service, snapshot, task types), migration 027
- Worker manager: internal/worker/ (manager, worker — full + lightweight modes)
- Worktree isolation: internal/worktree/ (manager, noop — git worktree lifecycle)
- Container wired: Coord, Tasks, Workers, Worktrees fields
- API endpoints: /api/tasks/*, /api/workers/*

CURRENT: Phase C — Memory & Continuity
- Check Cortex for type/view registry changes needed
- MemoryService, extraction (PostCompact + per-turn), memory tools (opt-out)

FRONTEND TODOs (accumulated):
- Tool load preferences UI (GET/PUT /api/tools/load-preferences + GET /api/tools/all)
- Task tracking UI (GET/POST /api/tasks/*)
- Worker status UI (GET /api/workers/*)

PRINCIPLES:
- Consult before architecture decisions
- Greenfield modules alongside old code, migrate when ready
- go build + go vet + go test clean after every task
- Always use cerberus_rebuild for deployment, not go build directly
- Quality over speed. Polished software, maintainable patterns.
- Brand package: use brand.* constants, never hardcode app name/identity

Test suite: all passing (zero failures)
```

## Frontend Agent

```
Boot nanite-frontend

Rebrand from Conduit → Nanite — backend complete, frontend Wave 3 in progress.

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- .agentrc/agents/frontend.md — full project context
- CLAUDE.md — envelope system warnings

REBRAND CONTEXT:
- Backend fully rebranded: module github.com/hollis-labs/nanite
- Envelope protocol renamed: nanite-envelope (was conduit-envelope)
- Tool names renamed: nanite_* (was conduit_*)
- Env vars renamed: NANITE_* (was CONDUIT_*)
- Brand package at internal/brand/brand.go — frontend should mirror with ui/src/brand.ts

COMPLETED (vNext frontend, 2026-04-03):
- P0-P3 debug panels, approval cards, broker inspector, compaction divider
- Tool call drawer with session-scoped retention
- All 6 anti-patterns resolved
- 23 shadcn components installed

Remaining future work:
- Phase 7: Dynamic plugin loading (subprocess + JSON-RPC backend, URL ESM frontend)
- Additional envelope card designs as new plugins are built
```
