---
name: surface-agent-setup
description: How a Surface app hosts a Nanite agent - the app's seat, module profiles, per-module bindings, and what the app supplies versus the folder.
---
# Nanite agents in a Surface app

Each Surface app runs one Nanite agent in its own process
(`SURFACE_AGENT_RUNNER=nanite`). Read the app repo's `docs/agent.md` for
the current details; this is the working summary.

## What the app supplies (not the folder)

| What | Where |
|---|---|
| Model, gateway, key | `~/.config/<app>/agent.json`: `model`, `gateway_url`, `key_file` (0600, under home) |
| Tools | The app's own tools with their levels: read/act run, write asks (a standing "Always" is kept), confirm asks every time |
| Nanite built-in groups | `"builtins": [...]` in agent.json (files-read, skills, subagents, ...); none by default |
| Limits override | positive `max_tokens` / `max_steps` in agent.json win over host.yaml |
| Auto mode | `auto_approve: true` in agent.json, testing only, the owner's call |
| MCP servers | the agent folder's `.mcp.json` (the agent.json `mcp` field is reserved) |

## Three ways to give an app an agent

1. **The app's seat**: `<agents.dir>/<app>/` with `agent.md` and `host.yaml`
   (default `agents/` beside agent.json). Surface writes a starter once and
   never touches it again.
2. **A module profile**: `modules/<id>/profile/` inside the module's code: an
   agent folder WITHOUT host.yaml, .mcp.json, permissions.yaml,
   providers.yaml or identity.yaml. It brings words and helpers (agent.md
   body, context, skills, variants, tool packages), never authority. While
   the module's window is in front (or the profile is pinned), each turn runs
   as the seat with the profile laid over it.
3. **A per-module binding**: an app setting maps a module id to an agent
   folder on disk, e.g. `~/.config/<app>/agents/<name>/`, which replaces
   that module's built-in profile. It is checked when bound and at boot.
   The agent's tools, levels and grants do not change. (Being built; check
   the app's docs for the exact setting name.)

All profiles share one conversation, front desk, mailbox, tools, approvals
and identity: one app, one agent, many faces.

## Checks after a change

- `nanite validate <folder>` passes.
- Restart the app if agent.json changed; folder edits apply on the next turn.
- The startup log and `/api/agent/setup` show the runner, model and auto
  mode. `SURFACE_AGENT_DEBUG=1` logs each turn's context block.
