# CONDUIT — Chat Harness

Agent-agnostic multi-agent chat harness for Fragments Engine.

## Build & Test

```bash
# Backend
go build ./cmd/conduit/
go test ./...

# Frontend
cd ui && npm install && npm run build

# Run (default port 8090)
./conduit serve -port 8090 -db ./conduit.db -dev
```

## Architecture

- `cmd/conduit/` — Entry point
- `internal/api/` — HTTP API handlers
- `internal/chat/` — Chat engine (orchestration, context, delegation)
- `internal/mcp/` — MCP client integration
- `internal/provider/` — LLM provider abstractions (Anthropic, OpenAI, Ollama)
- `internal/store/` — SQLite persistence layer
- `internal/config/` — Config loader (user + project merge)
- `ui/src/` — React frontend
- `config/agents/` — Agent profile definitions
- `docs/` — Architecture docs
