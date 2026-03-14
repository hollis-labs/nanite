# Tool-First Architecture

> Fragments Engine uses service-backed tools as the primary interface. The GUI/apps are the primary harness. CLI tools are consumers.

## Core Principle

**All state writes go through Fragments Engine services.** Never write state directly to files.

| Service | Owns | MCP tools |
|---------|------|-----------|
| Volon   | Tasks, sprints, projects | `volon_task_create`, `volon_sprint_create`, `volon_task_transition`, etc. |
| Cortex  | Context, PCC, namespaces | `context_write`, `context_typed_write`, `context_pack`, etc. |
| Hadron  | Blueprints, automation, pipelines | `hadron_run_enqueue`, `hadron_blueprint_get`, etc. |

## MCP Is the Primary Interface

- Agents interact with services exclusively via MCP tools
- Services own the database; MCP calls are the write path
- `project_id` on each MCP call scopes the operation to the correct project

## One Central Database Per Service

- Volon: one SQLite DB for all tasks/sprints/projects across all Fragments Engine projects
- Cortex: one SQLite DB for all context records
- Hadron: one SQLite DB for all blueprint runs/schedules
- Mentat: one SQLite DB for agents, skills, prompts, workflows, MCP servers
- No per-project databases. No per-repo SQLite files.

## File Cache Is Read-Only

| Path | Status | Purpose |
|------|--------|---------|
| `.agentrc/tasks/` | Read-only cache | Exported snapshots from Volon DB |
| `.agentrc/pcc/` | Read-only cache | Exported snapshots from Cortex |
| `.agentrc/bootstrap.md` | Agent-writable | Session state (exception: agents maintain this) |

- Caches are populated by sync hooks/skills, not by agents writing directly
- Agents read caches for fast local access but write through MCP

## CLI Is a Consumer

- CLI does not create separate databases or file stores
- CLI and MCP tools are peer consumers of the same backend

## Hooks Enforce the Pattern

- `pre-tool-use` hook blocks direct writes to managed paths (`.agentrc/tasks/`, `.agentrc/pcc/`, etc.)
- Forces agents to use MCP tools for state changes
- Allowlist for paths agents may write directly (bootstrap, logs)
