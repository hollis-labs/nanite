---
from: lead
to: toolbroker-agent
type: handoff
priority: high
timestamp: 2026-03-14T17:00:00Z
subject: Universal ToolBroker — TASK-165
---

## Mission

Make ToolBroker the universal tool selection layer for ALL consumers, not just Mentat's chat engine. Currently only Mentat wraps it. Volon's GUI chat, Hadron, and any future consumer should route tool calls through ToolBroker for intent-aware selection, budget enforcement, and progressive discovery.

## Task

**TASK-20260314-165**: Make ToolBroker the universal tool selection layer for all consumers (Priority A)

## Current State

- **ToolBroker library**: `~/Projects-apps/tiamat-tool-broker/` (will become `core/broker` later)
  - `broker.LocalBroker` — intent detection, rule engine, tool selection
  - `default-rules.yaml` — intent-to-tool mapping rules
  - Progressive discovery: `request_tools` meta-tool pattern

- **Mentat's consumer**: `~/Projects-apps/mentat/internal/toolclient/` (recently renamed from toolbroker/)
  - `ToolClient` struct wraps `broker.LocalBroker`
  - Called by `internal/chat/engine.go` during chat turns
  - Implements: tool selection, permission enforcement, MCP coordination, progressive discovery

- **Volon's chat**: `~/Projects-apps/volon/internal/runtime/chat/service.go` (recently renamed to CompletionService)
  - Has its OWN provider+tool logic — does NOT use ToolBroker
  - Separate tool resolution, no intent-based selection, no progressive discovery

## What To Do

1. **Audit all tool call paths** across the portfolio:
   - Mentat chat engine (already uses ToolBroker via ToolClient) ✅
   - Volon CompletionService (does NOT — has own tool logic) ❌
   - Any CLI tools that call MCP directly
   - Any direct MCP consumers

2. **Design the universal interface** in `tiamat-tool-broker/`:
   - The library should be consumable by any Go project with minimal setup
   - Interface: `SelectTools(intent string, budget int) → []ToolDefinition`
   - Interface: `RequestTools(keywords []string) → []ToolDefinition` (progressive discovery)
   - Config: rule file path, MCP server list, budget defaults

3. **Create a Volon ToolClient** (parallel to Mentat's):
   - `~/Projects-apps/volon/internal/toolclient/client.go`
   - Wire into CompletionService for tool selection
   - Share the same `default-rules.yaml`

4. **Document the integration pattern** so future consumers know how to adopt

## File Coordination

**YOU OWN these files:**
- `~/Projects-apps/tiamat-tool-broker/` — the library itself
- `~/Projects-apps/volon/internal/toolclient/` — new package you create
- `~/Projects-apps/volon/internal/runtime/chat/service.go` — wire in ToolClient

**DO NOT TOUCH:**
- `~/Projects-apps/mentat/internal/toolclient/` — Mentat's existing consumer works fine
- `~/Projects-apps/mentat/internal/chat/engine.go` — the ContextBroker agent may touch this
- Any naming/renaming — that's done, don't redo it

**COORDINATION with contextbroker-agent:**
Both of you may need to update `mentat/internal/chat/engine.go` as the main consumer. **You go first on engine.go if needed.** Send a message to contextbroker-agent when you're done with any engine.go changes so they don't conflict:
- `/send-message contextbroker-agent info engine.go clear — I'm done with engine.go changes, you can proceed.`

## Communication

- Your session ID: `toolbroker-agent`
- Project lead inbox: `.agentrc/inbox/lead/`
- ContextBroker agent inbox: `.agentrc/inbox/contextbroker-agent/`
- Check your inbox before each major step
- Send status after: (1) audit complete, (2) interface designed, (3) Volon client wired, (4) done

## Constraints

- Use `go install` not `go build` for any binary updates
- Follow naming conventions: `docs/architecture/naming-conventions.md`
- The existing Mentat ToolClient is the reference implementation — follow its pattern
- Don't change the default-rules.yaml yet (TASK-162 handles that separately)
- Run `go build ./...` and `go test ./...` after every change
