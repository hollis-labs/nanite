---
from: lead
to: contextbroker-agent
type: handoff
priority: high
timestamp: 2026-03-14T17:00:00Z
subject: Universal ContextBroker — TASK-168
---

## Mission

Build ContextBroker as the universal context retrieval layer — the context equivalent of ToolBroker. Any agent/consumer calls it with an intent + budget and gets back a focused, multi-source context packet. Currently context retrieval is fragmented: Mentat has a local prompt assembler, Cortex has its own query planner, and nothing connects them.

## Task

**TASK-20260314-168**: Make ContextBroker the universal context retrieval layer for all consumers (Priority A)

## Current State

- **Cortex's query planner**: `~/Projects-apps/cortex/internal/contextapi/broker_handler.go`
  - Recently renamed: `PlannerConfig`, `ContextPlanRequest`, `handleContextPlan`
  - Does: intent → namespace patterns → budget-bounded fetch from Cortex records ONLY
  - 4 intents: resume_task, boot_project, review_session, custom
  - MCP tools: `context_broker_plan`, `context_broker_fetch` (names kept for backward compat)
  - This is ONE source adapter, not the universal broker

- **Mentat's prompt assembler**: `~/Projects-apps/mentat/internal/chat/context_client.go`
  - Recently renamed from broker.go: `ContextClient` struct
  - Does: load session history + system prompt + token budgeting
  - Queries ONLY Mentat's local SQLite — no external context
  - This should CONSUME the universal ContextBroker, not be it

- **Other context sources** (not yet integrated):
  - PCC files (`.agentrc/pcc/global/`) — project context cache, 6 files per project
  - Volon tasks/sprints — current work state
  - Nanite vaults — knowledge items via API
  - Session history — chat messages from current/previous sessions
  - Memory index — `~/.claude/projects/*/memory/MEMORY.md`

## What To Do

1. **Design the ContextBroker package** — new package, likely at `~/Projects-apps/mentat/internal/contextbroker/` for now (will move to `core/context` during consolidation):
   ```go
   type ContextBroker struct {
       sources []ContextSource
       budget  BudgetConfig
   }

   type ContextSource interface {
       Name() string
       Fetch(intent Intent, budget int) ([]ContextItem, error)
   }

   type Intent struct {
       Type     string   // resume_task, boot_project, write_code, debug_issue, etc.
       Keywords []string // extracted from user query
       Scope    string   // project_id, namespace, etc.
   }

   type ContextPacket struct {
       Items       []ContextItem
       Manifest    Manifest // sources queried, items returned, truncation info
       TokenEstimate int
   }
   ```

2. **Build source adapters** (at minimum):
   - **CortexSource** — calls Cortex MCP `context_broker_fetch` with intent
   - **PCCSource** — reads `.agentrc/pcc/global/` files
   - **VolonSource** — calls Volon MCP for current task/sprint state
   - **SessionSource** — reads recent session messages from Mentat's store

3. **Wire into Mentat's ContextClient**:
   - `ContextClient.AssembleContext()` calls `ContextBroker.Fetch(intent, budget)` first
   - Injects returned context into the system prompt
   - Existing session history + prompt template logic stays

4. **Add new intent types** (beyond Cortex's 4):
   - `write_code` — fetch conventions, architecture, related implementations
   - `debug_issue` — fetch error context, related fixes, known pitfalls
   - `plan_feature` — fetch roadmap, related epics, architecture decisions
   - `recall_decision` — fetch ADRs, meeting notes, rationale

## File Coordination

**YOU OWN these files:**
- `~/Projects-apps/mentat/internal/contextbroker/` — new package you create
- `~/Projects-apps/mentat/internal/chat/context_client.go` — wire in ContextBroker

**WAIT before touching:**
- `~/Projects-apps/mentat/internal/chat/engine.go` — the ToolBroker agent may be modifying this. **Wait for their "engine.go clear" message** before making changes. Check `.agentrc/inbox/contextbroker-agent/` for the signal.

**DO NOT TOUCH:**
- `~/Projects-apps/cortex/internal/contextapi/` — Cortex's planner is fine as-is, consume it via MCP
- Any naming/renaming — that's done
- `~/Projects-apps/tiamat-tool-broker/` — ToolBroker agent's territory

## Communication

- Your session ID: `contextbroker-agent`
- Project lead inbox: `.agentrc/inbox/lead/`
- ToolBroker agent inbox: `.agentrc/inbox/toolbroker-agent/`
- Check your inbox before each major step — especially before touching engine.go
- Send status after: (1) package designed, (2) source adapters built, (3) wired into ContextClient, (4) done

## Constraints

- Use `go install` not `go build` for any binary updates
- Follow naming conventions: `docs/architecture/naming-conventions.md`
- Budget estimation: use `(len(payload) + 3) / 4` heuristic (same as Cortex and Mentat)
- Cortex's MCP tools are the external interface — don't bypass them with direct DB access
- PCC files are read-only — never write to `.agentrc/pcc/global/`
- Run `go build ./...` and `go test ./...` after every change
