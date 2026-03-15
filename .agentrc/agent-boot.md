---
type: agent-boot
version: 1
updated_at: 2026-03-11
---

# Agent Boot — Mentat

## What is CONDUIT and Mentat

**CONDUIT** is the agent-agnostic chat harness for Fragments Engine. It is the application — a Go backend + React frontend that provides multi-agent AI chat with MCP integration, delegation, and context continuity. Binary: `conduit`. Module: `github.com/hollis-labs/conduit`.

**Mentat** is a Special Agent that runs inside CONDUIT. It is NOT an application — it is an agent profile (`config/agents/mentat.yaml`) loadable by any compatible chat system. Mentat provides cognitive aid, portfolio orchestration, planning, and cross-project coordination. There is no "Mentat Chat" — there is only CONDUIT with the Mentat agent loaded.

## Ground truth

- `agentrc.yaml` — system configuration
- Volon MCP — canonical task/sprint management (volon_tasks_list, volon_task_transition, etc.)
- `.agentrc/bootstrap.md` — iteration state cache (read-only, Volon is truth)
- `.agentrc/pcc/global/` — project context cache

## Core rules

- **Single writer**: Only one agent session writes state at a time.
- **Volon is the task system of record**: All work tracked as Volon tasks. Update immediately.
- **Ground truth in Volon and files, not chat**: Re-ground from Volon MCP and repo artifacts.
- **Deterministic outputs**: Every tick must leave a clear "next action" in a task update.
- **Record decisions first**: Write an ADR in `adr/` before proceeding with non-trivial decisions.
- **Index before scan**: Use reference map before reading individual files or globbing.

## Reference map

| What you need | Where to look |
|---|---|
| Current state | `.agentrc/bootstrap.md` → Volon MCP |
| System config | `agentrc.yaml` |
| Conduit config | `config/mentat.yaml` |
| Agent profiles | `config/agents/*.yaml` |
| Managed projects | `config/repos.yaml` |
| Behavior rules | `docs/process/00_behavior-rules.md` |
| Architecture | `docs/architecture/ARCHITECTURE.md` |
| Project context cache | `.agentrc/pcc/global/` |
| ADRs | `adr/` |
| Backend code | `cmd/`, `internal/` |
| Frontend code | `ui/src/` |

## Your role

Load the addendum at `.agentrc/boot/<profile>.md` for your specific role, write paths, and boot confirmation format.
