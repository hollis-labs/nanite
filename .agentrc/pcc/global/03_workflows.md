---
intent: pcc_global
project: mentat
updated_at: "2026-03-13"
---

# Workflows

## Build

```bash
# Backend
go build ./cmd/mentat/

# Frontend
cd ui && npm install && npm run build
```

## Run

```bash
# Development mode (default port 8090)
./mentat serve -port 8090 -db ./mentat.db -dev
```

## Test

```bash
go test ./...
```

## Agent Boot

1. Read `.agentrc/agent-boot.md` (core rules)
2. List `.agentrc/boot/*.md` for available profiles
3. If one profile, auto-select; if multiple, ask user
4. Read selected profile (e.g., `orchestrator.md`, `worker.md`)
5. Read `.agentrc/bootstrap.md` (current iteration state)
6. Emit boot confirmation and begin work

## Service Management

- Mentat and dependent services managed via Cerberus MCP
- MCP servers configured in `~/.claude.json` (user scope)
- Services: Volon (Postgres), Hadron, Cortex, Cerberus

## Skills (11)

mentat-escalate, mentat-hadron-run, mentat-inbox-tick, mentat-project-map, git-status-all, health-check, pcc-sync, task-sync, commit-all, pcc-refresh-all, standup

## Slash Commands (14)

bootstrap-update, archive-sprint, pause-task, resume-task, pcc-refresh, sprint-end, git-status-all, health-check, pcc-sync, task-sync, commit-all, pcc-refresh-all, standup, commit-all

## Context Sync

- PCC refresh via Cortex MCP or `/pcc-refresh` command
- Recurring sync target: 2AM/2PM daily via Hadron pipeline
- Envelope guard blocks direct writes to `.agentrc/pcc/global/`

## Evidence
- Last refreshed: 2026-03-13
- Sources: CLAUDE.md, .agentrc/ directory, memory
