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

All Beta Release TODO (§1-9), Plugin Evolution (Phases 1-8, except 7), and all 6 anti-patterns resolved.
Composer menus (model picker, slash commands, file mentions) polished 2026-03-29.
Working on feature/plugin-evolution-profile-polish branch.

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- .agentrc/agents/frontend.md — full project context (includes Beta TODO + anti-patterns)
- CLAUDE.md — envelope system warnings

All 6 anti-patterns resolved:
1. Loose TypeScript (as any) — removed
2. Duplicate API — unified listAgents returns AgentProfile[]
3. Long deps (17→2) — store actions via getState()
4. Map mutations — immutable copies
5. Magic event strings — SSE constants
6. Global errorCounter — removed

Remaining future work:
- Phase 7: Dynamic plugin loading (subprocess + JSON-RPC backend, URL ESM frontend)
- Additional envelope card designs as new plugins are built

shadcn components available (23 total):
  alert-dialog, avatar, badge, button, card, command, context-menu,
  dialog, dropdown-menu, empty, input, kbd, popover, scroll-area,
  select, separator, sheet, skeleton, spinner, switch, tabs, textarea, tooltip
```

## Phase 7 — Backend Session Prompt

```
Boot conduit-backend

TASK: Phase 7 backend — Dynamic Plugin Loading via subprocess + JSON-RPC

This is the backend half of Phase 7 from the plugin evolution plan.
See docs/architecture/plugin-evolution-plan.md for full context.

CURRENT STATE:
- Phases 1-6, 8 complete. Plugins are Go code compiled into the binary.
- Plugin host interface: internal/chat/plugin.go (Host interface, PluginManager)
- Plugin SDK: internal/plugin/sdk/ (RegisterCommand, RegisterEnvelope, RegisterSlot, etc.)
- Builtin plugins: internal/plugin/builtin/ (bookmarks, giphy, sprint-planning, etc.)
- Plugin management API: internal/api/plugins.go (install/uninstall/enable/disable)
- Plugin manifests: plugins/*/plugin.yaml

GOAL: Allow plugins to run as separate processes communicating via JSON-RPC.
- Plugin subprocess launcher in internal/plugin/subprocess/
- JSON-RPC protocol (stdin/stdout or unix socket) for Host ↔ Plugin communication
- Plugin lifecycle: start, health check, restart on crash, graceful shutdown
- The Host interface methods (RegisterCommand, RegisterEnvelope, EmitEvent, etc.)
  must be callable over JSON-RPC so external plugins can use the same SDK
- Security: process isolation, resource limits, timeout enforcement

DESIGN CONSTRAINTS:
- Must be backward-compatible — existing compiled-in plugins keep working unchanged
- JSON-RPC transport should be swappable (stdio initially, unix socket later)
- Plugin process receives its config via JSON-RPC init handshake
- Events from host to plugin: JSON-RPC notifications (no response expected)
- Plugin manifest (plugin.yaml) gains a `runtime: subprocess` field

DO NOT touch frontend code. Backend only.
Always use cerberus_rebuild for deployment, not go build directly.
```

## Phase 7 — Frontend Session Prompt

```
Boot conduit-frontend

TASK: Phase 7 frontend — Dynamic Plugin UI Loading via URL ESM

This is the frontend half of Phase 7 from the plugin evolution plan.
See docs/architecture/plugin-evolution-plan.md for full context.

CURRENT STATE:
- All plugin UI is baked in at build time via generated registries:
  - ui/src/generated/plugin-envelopes.ts — lazy(() => import()) for envelope cards
  - ui/src/generated/plugin-widgets.ts — lazy(() => import()) for widgets
  - ui/src/generated/plugin-slot-components.ts — lazy(() => import()) for slot views
  - ui/src/generated/plugin-config-components.ts — custom config field overrides
- ADR-002: single registry with source field (core vs plugin)
- Scripts: scripts/generate-plugin-imports.mjs regenerates plugin entries at build time
- Envelope/widget components live in ui/src/components/chat/envelopes/ and ui/src/components/plugins/

GOAL: Allow plugins to provide UI components loaded at runtime (not build time).
- URL-based ESM loading — dynamically import() plugin UI modules from a URL
- Backend will serve plugin UI bundles (e.g., GET /api/plugins/{name}/ui/bundle.js)
- Runtime component registry — register envelope/widget components from loaded ESM
- Plugin dev server integration — hot-reload plugin UI during development
- Fallback: if dynamic load fails, fall back to build-time registry entry if one exists

DESIGN CONSTRAINTS:
- Must be backward-compatible — existing build-time registries keep working
- Dynamic imports happen AFTER the plugin list loads (api.listPlugins)
- Each plugin ESM module exports a register() function that receives a registry API
- The registry API lets plugins call: registerEnvelope(type, component),
  registerWidget(id, component), registerSlotComponent(name, component)
- Security: CSP-safe loading, no eval(), modules served from same origin only
- Error boundary per dynamically loaded component (don't crash the app)
- Recover mode skips all dynamic loads (core-only)

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- .agentrc/agents/frontend.md — full project context
- CLAUDE.md — envelope system warnings (registries are NOT auto-generated despite header)

DO NOT touch backend Go code. Frontend only.
```
