# Mentat Chat — agent boot

## Agent Auto-Boot Override

This repo boots as `worker`. **Skip profile selection** — go directly to:

4. Read `.agentrc/agent-boot.md`
5. Read `.agentrc/boot/worker.md`
6. Read `.agentrc/bootstrap.md`
7. Follow the worker profile instructions — emit boot confirmation and begin work

## Project Overview

Mentat Chat is a conversational AI interface with a Go backend and React/TypeScript frontend. It provides a chat UI that connects to multiple LLM providers via MCP, with conversation persistence, message truncation, and workflow orchestration.

## Build & Test

```bash
# Backend
go build ./cmd/mentat-chat/
go test ./...

# Frontend
cd ui && npm install && npm run build
```

## Architecture

- `cmd/mentat-chat/` — Entry point
- `internal/api/` — HTTP API handlers
- `internal/chat/` — Chat session management
- `internal/mcp/` — MCP client integration
- `internal/provider/` — LLM provider abstractions
- `internal/server/` — HTTP server setup
- `internal/store/` — SQLite persistence layer
- `internal/truncate/` — Message truncation logic
- `internal/workflow/` — Workflow orchestration
- `ui/src/components/` — React UI components
- `ui/src/hooks/` — React hooks
- `ui/src/lib/` — Utility functions
- `ui/src/stores/` — Zustand state stores
