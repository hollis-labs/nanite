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

CURRENT: Phase 5 — Agent System (decisions doc §8)
- MD-based agent definitions, file discovery (6 locations), seed.go cleanup, worktree isolation

PRINCIPLES:
- Consult before architecture decisions
- Greenfield modules alongside old code, migrate when ready
- go build + go vet + go test clean after every task
- Always use cerberus_rebuild for deployment, not go build directly
- Quality over speed. Polished software, maintainable patterns.
- Brand package: use brand.* constants, never hardcode app name/identity

Pre-existing test failures (not blockers):
- server.TestAuthMiddlewareEnabled: returns 200 instead of 401
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
