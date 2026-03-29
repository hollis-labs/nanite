# Session Boot Prompt

## Backend Agent

```
Boot conduit-backend

Plugin evolution plan complete (all 8 phases). See docs/architecture/plugin-evolution-plan.md.

Backend changes (2026-03-29):
- All plugin evolution phases 1, 2a, 3a, 5a, 5b, 6, 8a, 8b complete
- Phase 6: connector dispatch, health/retry, event streaming
- Phase 8: keybindings, auto-triggers, custom actions (DB + CRUD + slash cmd registration)
- Project CRUD: added GetProject, handleUpdateProject (PUT), handleDeleteProject (DELETE)
  in internal/store/workspaces.go and internal/api/workspaces.go
  Routes: PUT/DELETE /api/workspaces/{wid}/projects/{pid}

Pre-existing test failures (not blockers):
- server.TestAuthMiddlewareEnabled: returns 200 instead of 401
- plugin/builtin/email and plugin/builtin/teams: missing connector modules

Always use cerberus_rebuild for deployment, not go build directly.
```

## Frontend Agent

```
Boot conduit-frontend

All Beta Release TODO (§1-9) and Plugin Evolution (Phases 1-8, except 7) complete.
Audited and verified 2026-03-29. Working on main.

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- .agentrc/agents/frontend.md — full project context (includes Beta TODO + anti-patterns)
- CLAUDE.md — envelope system warnings

Remaining anti-patterns (5 of 6 — #4 Map mutations was fixed):
1. Loose TypeScript: `(a as any).is_primary` in AppShell.tsx:80
2. Duplicate API: listAgentProfiles() and listAgents() both hit /api/agents
3. Long deps: useChat.sendMessage has 17 dependencies
5. Magic event strings: SSE types hardcoded in useChat.ts
6. Global errorCounter: module-scoped in useChat.ts:10

Remaining future work:
- Phase 7: Dynamic plugin loading (subprocess + JSON-RPC backend, URL ESM frontend)
- Tag autocomplete: tag-input.tsx exists, needs TipTap Mention extension integration
- Plugin detail view page (tool and server detail views exist)
- Additional envelope card designs as new plugins are built

shadcn components available (23 total):
  alert-dialog, avatar, badge, button, card, command, context-menu,
  dialog, dropdown-menu, empty, input, kbd, popover, scroll-area,
  select, separator, sheet, skeleton, spinner, switch, tabs, textarea, tooltip
```
