---
from: lead
to: all
type: handoff
priority: high
timestamp: 2026-03-14T13:00:00Z
subject: Conduit Foundation — Iteration 3 scope and context
---

## Conduit Foundation Sprint — Assigned to conduit-agent

### What You're Building

Conduit is the chat application for Fragments Engine. It's currently called "mentat" in the codebase (repo: ~/Projects-apps/mentat/). The rename to "conduit" is planned (ADR-013) but NOT part of this sprint — focus on making it work, not renaming it.

### Your Scope

**Epic: EPIC-20260314-65311 (Demo-Ready Conduit E2E Polish)**

Priority tasks:
1. TASK-20260314-070 — Multi-stage Dockerfile (Go + Node build)
2. TASK-20260314-071 — Wire delegation flow end-to-end (Mentat agent spawns worker)
3. TASK-20260314-072 — Seed Cortex with representative demo records
4. TASK-20260314-074 — Create scripted demo workflow
5. TASK-20260312-61257 — Fix tool call loop (repeat blocking, short-circuit duplicates)

### Key Context

- **Go backend**: `cmd/mentat/`, `internal/` — HTTP API, chat engine, MCP integration
- **React frontend**: `ui/src/` — Chat UI, settings
- **Architecture**: `docs/architecture/ARCHITECTURE.md`
- **ADRs**: `adr/` — especially ADR-013 (Conduit separation), ADR-019 (Volon chat convergence)
- **Provider abstraction**: `internal/provider/` — Anthropic, OpenAI, Ollama
- **Chat engine**: `internal/chat/engine.go` — the core orchestration loop
- **MCP integration**: `internal/mcp/` — tool transports, manager

### Constraints

- Do NOT rename mentat → conduit yet. That's a separate epic.
- Do NOT refactor naming (TASK-170 handles that separately).
- The ToolBroker (`internal/toolbroker/`) currently blanket-excludes hadron_bp_* tools — don't change this, it's tracked separately.
- Mentat's `internal/chat/broker.go` is the ContextBroker CLIENT, not the universal broker. See TASK-168.
- Use `go install ./cmd/mentat/` (not `go build`) when updating binaries that MCP uses.

### Communication

- Your session ID: `conduit-agent`
- Project lead inbox: `.agentrc/inbox/lead/`
- Use `/send-message lead <type> <subject> — <body>` to communicate
- Check `.agentrc/inbox/conduit-agent/` and `.agentrc/inbox/broadcast/` for messages
- **PING THE OWNER** before any rename/move operations — they want to review end-state naming/locations

### What the Lead is Doing (Parallel)

Iterations 1 & 2: Quality gates (golangci-lint, Lefthook across all projects), task flow enforcement (sprint/epic auto-close cascade), naming cleanup, Volon schema fixes. This work is in DIFFERENT files than your scope — no conflicts expected.

### Dependencies

- Cortex MCP is working (contextd binary fixed, seeded with 12 records)
- Volon MCP is working (tasks, sprints, epics all operational)
- Module renames are done (hollis-labs/otel, etc.)
- E2E test infrastructure is rebuilt (dynamic ports, orphan cleanup)
