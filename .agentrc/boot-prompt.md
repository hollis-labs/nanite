# Session Boot Prompt

## Backend Agent

```
Boot conduit-backend

This is a beta release prep session. Primary TODO is in .agentrc/agents/backend.md
under "Beta Release TODO".

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

This is a beta release prep session. Primary TODO is in .agentrc/agents/frontend.md
under "Beta Release TODO (Frontend)".

Context files to read:
- .agentrc/agents/frontend.md — full project context + beta TODO
- docs/pty-bridge-evolution.md — scroll to "Frontend — Remaining" for PTY-specific items
- CLAUDE.md — envelope system warnings (frontend registry must match backend types)

Backend APIs available:
- GET /api/settings, PUT /api/settings — user settings (partial merge)
  Now includes: developer_mode (bool), recover_mode (bool)
- GET /api/processes/health — active CLI processes with uptime/idle
- POST /api/processes/kill-stale — kill hung processes
- GET /api/sessions/{id}/metrics — per-session execution snapshots
- GET /api/metrics/executions?limit=N — recent executions across all sessions
- GET /api/metrics/utility — aggregated utility call comparison
- GET /api/metrics/utility/log?limit=N — individual utility call records
- GET /api/providers — now includes OpenRouter + OpenZen
- GET /api/models — now includes OR/OZ model variants

Previously blocked, now unblocked (2026-03-28):
- Config override components: developer_mode flag available in GET /api/settings
- Recover mode: recover_mode flag available in GET /api/settings
- Plugin widget mount points: no backend dependency

Key things already done (don't redo):
- Settings UI, preferences, shortcuts, dynamic models, adapter badges, tool call
  drawer, unified event stream — all in commits c28e0c7 and de0bf77
- Backend: subprocess bridge, fallback chain, process tracking, observability

The backend agent may be working in parallel. Coordinate via the branch
(feature/pty-bridge-frontend). Commit frequently to stay synced.
```
