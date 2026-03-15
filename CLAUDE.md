---
autoboot: true
default profile: meta-agent
---
# Mentat — agent boot

The user wants you to autoboot on start without having to prompt. The  user is aware it will take time. Tell the user you are initiating the Mentat boot sequence. Then proceed to follow the steps below.

## Agent Auto-Boot

This repo supports multiple boot profiles. Follow the standard auto-boot sequence:

1. Read `.agentrc/agent-boot.md`
2. List `.agentrc/boot/*.md` for available profiles
3. If one profile → auto-select; if multiple → ask user
4. Read `.agentrc/boot/<selected-profile>.md`
5. Read `.agentrc/bootstrap.md`
6. Follow the profile instructions — emit boot confirmation and begin work

## What Mentat Is

Mentat is the unified meta-agent and chat application for Fragments Engine. It combines:
- **Go backend**: HTTP API, chat engine, MCP integration, multi-provider LLM support, SQLite persistence
- **React frontend**: Chat UI, settings, agent management, workflow orchestration
- **CLI integration**: Portfolio orchestration, skills, slash commands, hooks

## Build & Test

```bash
# Backend
go build ./cmd/mentat/
go test ./...

# Frontend
cd ui && npm install && npm run build

# Run (default port 8090)
./mentat serve -port 8090 -db ./mentat.db -dev
```

## Architecture

- `cmd/mentat/` — Entry point
- `internal/api/` — HTTP API handlers
- `internal/chat/` — Chat engine (orchestration, context, broker)
- `internal/mcp/` — MCP client integration
- `internal/provider/` — LLM provider abstractions (Anthropic, OpenAI, Ollama)
- `internal/store/` — SQLite persistence layer
- `internal/builders/` — Agent/skill builders
- `internal/toolbroker/` — Tool selection and intent analysis
- `internal/workflow/` — Workflow orchestration
- `ui/src/` — React frontend
- `config/` — Mentat config (repos, service URLs)
- `docs/` — Architecture, process, planning docs
- `adr/` — Architecture decision records
