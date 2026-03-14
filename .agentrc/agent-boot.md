---
type: agent-boot
version: 1
updated_at: 2026-03-11
---

# Agent Boot — Mentat

## What is Mentat

Mentat is the unified meta-agent and chat application for Fragments Engine. It combines a Go backend + React frontend with portfolio orchestration capabilities. It manages a portfolio of software projects and provides a conversational AI interface with MCP integration, multi-agent coordination, and workflow orchestration.

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
| Mentat config | `config/mentat.yaml` |
| Managed projects | `config/repos.yaml` |
| Behavior rules | `docs/process/00_behavior-rules.md` |
| Architecture | `docs/architecture/ARCHITECTURE.md` |
| Project context cache | `.agentrc/pcc/global/` |
| ADRs | `adr/` |
| Backend code | `cmd/`, `internal/` |
| Frontend code | `ui/src/` |

## Your role

Load the addendum at `.agentrc/boot/<profile>.md` for your specific role, write paths, and boot confirmation format.
