# Session Boot Prompt

## Backend Agent

```
Boot conduit-backend

Agent Schema v2 implementation in progress. Phases 1-2 done, Phases 3-4 remain.
Read the plan doc: ~/.claude/plans/starry-tickling-tower.md for full context.

Context files to read:
- .agentrc/agents/backend.md — full project context + beta TODO
- docs/pty-bridge-evolution.md — PTY bridge status + remaining items
- CLAUDE.md — envelope system warnings, build/deploy rules

Completed (2026-03-28 session):
- Section 1 (Provider Expansion): All done. Gemini, Mistral, Azure, Copilot, Aider
  already existed. Added OpenRouter + OpenZen providers with seeds and tests.
- Section 2 (Plugin System): All done (config, connectors, hooks, widgets, generator, guide).
- Section 4 (Multi-Session Presence): Done except PTY tool-level (deferred).
- Section 5 (Artifacts): Done (auto-detect + metadata).
- Section 7 (Small Items): Done (utility from DB, agent_id in session creation,
  developer_mode/recover_mode flags).
- Env cleanup: Removed godotenv/.env, Volon/Mentat vars, dead API key defaults.
  API keys are keychain-only. Activity emitter rebranded from Volon to Engine.

Remaining for future sessions:
- Section 3 (Slash Commands) — port from Fragments v1, /status, /providers, plugin commands
- Section 6 (Agent Model / agentrc integration) — needs decision on 6b
- Env var migration — secrets to keychain, config to GUI settings.
  See memory: project_env_var_cleanup.md for the full list.

Pre-existing test failures (not blockers, just log them if you hit them):
- mcp.TestSelfToolsTransport_ListTools: expects 12 tools, gets 20
- server.TestAuthMiddlewareEnabled: returns 200 instead of 401
- plugin/builtin/email and plugin/builtin/teams: missing connector modules

Always use cerberus_rebuild for deployment, not go build directly.
```

## Frontend Agent

```
Boot conduit-frontend

Frontend polish phase 1 is mostly complete. Theme migration, sidebar redesign,
and project scope are done. Next focus is CRUD GUIs and remaining cleanup.

Branch: feature/frontend-polish-phase1 (not yet merged to main)

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference
- memory: project_frontend_polish.md — what's done, what remains
- .agentrc/agents/frontend.md — full project context
- CLAUDE.md — envelope system warnings

Design System (established 2026-03-28):
- Theme: CSS variables in index.css, light/dark via .light/.dark class on <html>
- Colors: ALWAYS use semantic tokens (bg-bg, text-fg, border-border, etc.)
  NEVER hardcode bg-zinc-*, text-zinc-*, border-zinc-* — light theme breaks
- Accent: fire engine red #dc2626 (bg-accent, text-accent)
- Status/toggle: blue #3B82F6 (bg-success, bg-toggle-on) — NOT green
- Composer: always-dark tokens (bg-composer, text-composer-fg, etc.) — same both themes
- Cards: rounded-xl border-border-subtle shadow-sm, two-section (header + footer)

Completed (2026-03-28 extended session):
- Semantic token migration: ~500 zinc refs replaced across 80+ files
  (chat area, envelopes, widgets, tool calls, errors, modals, menus)
- Chat composer redesign: always-dark with light typing area + dark toolbar
- Light mode highlight.js theme (github light scoped under .light)
- Sidebar redesign: "Sessions" → "Chats", project dropdown, 2-line compact
  chat items with MessageSquare icon + count badge, AdapterBadge (PTY/API)
- Removed NewSessionForm (creation overrides), removed tasks zone dead code
- Project dropdown with All Chats + project list + New Project modal
- Workspace switcher: hover chevron, accent ring, Add Workspace button
- Numbered list line break fix in markdown rendering

Remaining work:
1. Workspace CRUD GUI — proper create modal (replace window.prompt),
   settings page for edit/delete workspaces
2. Project CRUD GUI — settings page for edit/delete, project context files
   (PRD, spec, architecture docs as project-scoped artifacts)
3. Project backend gaps — GET/PUT/DELETE single project endpoints,
   session filtering by project_id (backend agent task)
4. SprintPlanningModal — move to plugin (not core)
5. Plugin architecture — A2A plugin, Sprint plugin, UI hooks/events system
6. Envelope card design refinement — colors still feel "Microsoft-ish",
   need more polished/professional palette

Reference implementations:
- ProviderManager.tsx — tabbed view with search/filter/sort + Variation F cards
- ShortcutsPanel.tsx — interactive click-to-edit cards
- CreateProjectModal.tsx — clean modal pattern for entity creation
- ProjectDropdown.tsx — dropdown selector with inline create
```
