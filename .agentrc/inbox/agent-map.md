# Active Agent Map

> Updated by the Project Lead. Check before starting work to understand who's doing what.
> Last updated: 2026-03-14T17:00:00Z

## Sessions

| Session ID | Role | Status | Scope | Files Owned |
|------------|------|--------|-------|-------------|
| **lead** | Project Lead | active | Orchestration, Iteration 4, planning | All — final authority |
| **toolbroker-agent** | Worker | active | TASK-165: Universal ToolBroker | tiamat-tool-broker/*, volon/internal/toolclient/* |
| **contextbroker-agent** | Worker | active | TASK-168: Universal ContextBroker | mentat/internal/contextbroker/*, mentat/internal/chat/context_client.go |
| **context-arch-agent** | Worker | active | EPIC-21360 S1: Boot & Directives | CLAUDE.md, MEMORY.md, .agentrc/boot/*.md |
| **tools-agent** | Worker | **done** | EPIC-11418 S2+S3: 21/21 tasks complete | .claude/skills/* (committed) |
| **conduit-agent** | Worker | idle | EPIC-65311 complete | — |

## File Coordination

| File | Owner | Others Must |
|------|-------|-------------|
| `mentat/internal/chat/engine.go` | toolbroker-agent (first), then contextbroker-agent | Wait for "engine.go clear" message |
| `tiamat-tool-broker/` | toolbroker-agent | Do not modify |
| `mentat/internal/contextbroker/` | contextbroker-agent | Do not modify |
| `.claude/skills/` (new files) | tools-agent | Do not modify existing skills |
| `volon/apps/gui/` | lead | Workers stay out |

## Communication

- All agents: check `.agentrc/inbox/<your_session_id>/` and `broadcast/` before each task
- Status updates → `.agentrc/inbox/lead/`
- Decisions needed → `/send-message owner question ...`
- Cross-agent coordination → `/send-message <other-agent> ...`

## How to Update This Map

Only the Project Lead updates this file. If you're a worker agent and your status changes (idle, blocked, done), message the lead and they'll update.
