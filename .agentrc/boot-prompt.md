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

This is a frontend polish session. The settings pages and shell are done.
The next focus is the chat area and envelope cards.

Branch: feature/frontend-polish-phase1 (not yet merged to main)

CRITICAL — Read these before touching any code:
- memory: feedback_ui_design_patterns.md — THE design system reference. Card patterns,
  color tokens, icon badges, and all conventions. Deviating = inconsistency.
- memory: project_frontend_polish.md — what's done, what remains
- .agentrc/agents/frontend.md — full project context
- CLAUDE.md — envelope system warnings

Design System (established 2026-03-28 polish session):
- Theme: CSS variables in index.css, light/dark via .light/.dark class on <html>
- Colors: ALWAYS use semantic tokens (bg-bg, text-fg, border-border, etc.)
  NEVER hardcode bg-zinc-*, text-zinc-*, border-zinc-* — light theme breaks
- Accent: fire engine red #dc2626 (bg-accent, text-accent)
- Status/toggle: blue #3B82F6 (bg-success, bg-toggle-on) — NOT green
- Cards: rounded-xl border-border-subtle shadow-sm, two-section (header + footer)
- Icons: neutral gray (bg-zinc-700/bg-zinc-300), NOT per-item brand colors
- Status: tiny dot (w-1.5 h-1.5 bg-success) next to name, NOT green text

Remaining work:
1. Chat area — ChatTranscript, ChatMessage, ChatComposer, MessageContent all still
   use hardcoded zinc colors. Need semantic token migration.
2. Envelope cards (25+ components in ui/src/components/chat/envelopes/) — all
   hardcoded dark theme. These are rich response cards and need careful migration.
3. Widget components (right rail) — hardcoded zinc
4. Modals (SprintPlanningModal, AgentRoster) — hardcoded zinc
5. Slash command menu — hardcoded zinc
6. Light theme: highlight.js theme needs conditional swap (github-dark vs github)
7. Delete ui/public/card-prototypes.html (prototype file, no longer needed)
8. Plugin architecture items from brain dump (A2A plugin, Sprint plugin, UI hooks)
9. Project scope feature — not started

Reference implementations for the card pattern:
- ProviderManager.tsx — tabbed view with search/filter/sort + Variation F cards
- ShortcutsPanel.tsx — interactive click-to-edit cards
- AgentProfileManager.tsx — entity cards with status dots
```
