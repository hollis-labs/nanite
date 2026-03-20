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

## Envelope System (critical — read before touching)

Envelopes are structured UI cards injected into chat messages. The system has two sides that **must stay in sync manually**:

- **Backend** (Go): `internal/chat/envelope.go` creates envelope types (`kb-result`, `ticket-confirmation`, etc.)
- **Frontend** (TypeScript): `ui/src/generated/plugin-envelopes.ts` maps type strings to React components

**The registry is NOT auto-generated** despite what the file header says. Adding a backend envelope type without a matching frontend registry entry causes the envelope to be **silently dropped** — no error, no warning, just a blank space where the card should be.

**When adding or refactoring envelope types:**
1. Create the React component in `ui/src/components/chat/envelopes/`
2. Add a lazy import entry in `ui/src/generated/plugin-envelopes.ts`
3. Verify the `data` shape the backend sends matches what the component expects
4. Test both the streaming path (SSE deltas) and the persisted path (page reload)

**Known envelope types** (as of 2026-03-20): `task-disposition`, `giphy-modal`, `document-viewer`, `report-card`, `task-complete-notification`, `sprint-planning-review`, `kb-result`, `ticket-confirmation`, `ticket-form`, `resolution-capture`
