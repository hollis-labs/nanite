# Session Boot Prompt

## Backend Agent

```
Boot conduit-backend

Backend is stable. Last major work: 7 frontend-unblock items (migration 018).

Completed (2026-03-29 session):
- Messages pagination: GET /api/sessions/{id}/messages?limit=N&offset=N
- Search endpoint: GET /api/search?q=...&workspace_id=...&project_id=...
- Session archive: PUT /api/sessions/{id} accepts { status: "archived" },
  GET /api/sessions excludes archived by default, ?include_archived=true to include
- Bookmark autotitle: POST /api/bookmarks/{id}/autotitle (calls utility model)
- Agent-project many-to-many: agent_projects table, CRUD endpoints
- Source "seed" → "system": seeder + migration
- Icon column: added to agents, skills, prompts, plugins tables

Pre-existing test failures (not blockers):
- mcp.TestSelfToolsTransport_ListTools: expects 12 tools, gets 20
- server.TestAuthMiddlewareEnabled: returns 200 instead of 401
- plugin/builtin/email and plugin/builtin/teams: missing connector modules

Always use cerberus_rebuild for deployment, not go build directly.
```

## Frontend Agent

```
Boot conduit-frontend

Frontend polish phase 2 is complete. Commit: 8564ea2 on main.

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- memory: project_frontend_polish.md — full completed/remaining breakdown
- .agentrc/agents/frontend.md — full project context
- CLAUDE.md — envelope system warnings

What was done (2026-03-29 session):
- 21 shadcn components installed (Dialog, Command, Skeleton, Separator, etc.)
- 10 hand-rolled modals migrated to shadcn Dialog
- Command palette (Cmd+K) with drill-down groups
- Chat search modal (Shift+Shift) with scope filters
- Navigation history stack + Escape-to-go-back
- Tool drawer: pull-tab centered under header, 80% width, drag to resize
- Agent roster: mini-cards, square avatars, shadcn Separator
- User profile menu popover in composer toolbar
- Left sidebar: shadcn DropdownMenu, infinite scroll, delete context menu
- Widget rail: session info fix, edit mode (pencil icon), bookmark autotitle
- Agent detail: inline click-to-edit, square avatar, MCP structured display
- Tag colors: deterministic hash-based 8-color palette
- Consistent buttons, "templates" → "prompts" rename
- Context menus on agent/skill/plugin cards
- Optimistic UI for pin/delete, skeleton loading sweep

Remaining work (prioritized):
1. Wire up backend-dependent features (all 4 endpoints are ready):
   a. Chat infinite scroll — ChatTranscript + useChat, use offset/limit
   b. Message-level search — update SearchModal to show message results
   c. Archive chat — add Archive to sidebar context menu, use status field
   d. Agent-project chips — add to agent detail view
2. Tag autocomplete with TipTap (reference ~/Projects-apps/nanite)
3. Icon field / Lucide icon picker on entity specs
4. Workspace CRUD GUI (proper create modal, settings page)
5. Project CRUD GUI (settings page, project context files)

shadcn components available (23 total):
  alert-dialog, avatar, badge, button, card, command, context-menu,
  dialog, dropdown-menu, empty, input, kbd, popover, scroll-area,
  select, separator, sheet, skeleton, spinner, switch, tabs, textarea, tooltip

Design System (established):
- Theme: CSS variables in index.css, light/dark via .light/.dark on <html>
- Colors: ALWAYS use semantic tokens (bg-bg, text-fg, border-border)
- Accent: fire engine red #dc2626 (bg-accent, text-accent)
- Status/toggle: blue #3B82F6 (bg-success, bg-toggle-on)
- Cards: rounded-xl border-border-subtle shadow-sm (Variation F)
- Tags: hash-based 8-color palette (TagPills component)
- Avatars: square with rounded-sm corners
```
