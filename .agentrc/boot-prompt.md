# Session Boot Prompt

## Backend Agent

```
Boot conduit-backend

This is a beta release prep session. All PTY bridge work is complete — see
docs/pty-bridge-evolution.md for what was done and what remains (two small backend
items at the bottom plus the full beta TODO).

Your primary TODO list is in .agentrc/agents/backend.md under "Beta Release TODO".
Work through tasks in section order (1-7). Flag any decisions needed — especially
section 6b (agentrc integration) which needs user input before implementation.

Context files to read:
- .agentrc/agents/backend.md — full project context + beta TODO
- docs/pty-bridge-evolution.md — PTY bridge status + remaining items
- CLAUDE.md — envelope system warnings, build/deploy rules

Key things already done this session (don't redo):
- Subprocess adapter, provider fallback chain, process tracking, health checks,
  concurrency limits, execution observability, utility call comparison log
- All in commits d532171 and 475bce4 on feature/pty-bridge-frontend

Pre-existing test failures (not blockers, just log them if you hit them):
- mcp.TestSelfToolsTransport_ListTools: expects 12 tools, gets 20
- server.TestAuthMiddlewareEnabled: returns 200 instead of 401
- plugin/builtin/email and plugin/builtin/teams: missing connector modules

Always use cerberus_rebuild for deployment, not go build directly.
```

## Frontend Agent

```
Boot conduit-frontend

This is a beta release prep session. The backend agent completed all PTY bridge
backend tasks and added several new APIs you can build against.

Your primary TODO list is in .agentrc/agents/frontend.md under "Beta Release TODO
(Frontend)". Work through tasks in section order (1-7).

Context files to read:
- .agentrc/agents/frontend.md — full project context + beta TODO
- docs/pty-bridge-evolution.md — scroll to "Frontend — Remaining" for PTY-specific items
- CLAUDE.md — envelope system warnings (frontend registry must match backend types)

New backend APIs available (built this session):
- GET /api/settings, PUT /api/settings — user settings (partial merge)
- GET /api/processes/health — active CLI processes with uptime/idle
- POST /api/processes/kill-stale — kill hung processes
- GET /api/sessions/{id}/metrics — per-session execution snapshots
- GET /api/metrics/executions?limit=N — recent executions across all sessions
- GET /api/metrics/utility — aggregated utility call comparison (provider, avg duration, errors)
- GET /api/metrics/utility/log?limit=N — individual utility call records

Key things already done (don't redo):
- Settings UI, preferences, shortcuts, dynamic models, adapter badges, tool call
  drawer, unified event stream — all in commits c28e0c7 and de0bf77
- Backend: subprocess bridge, fallback chain, process tracking, observability

The backend agent may be working in parallel. Coordinate via the branch
(feature/pty-bridge-frontend). Commit frequently to stay synced.
```
