# Agent Inbox — A2A Messaging

Simple file-based messaging between Mentat agent sessions.

## Structure

```
.agentrc/inbox/
├── README.md
├── broadcast/           # Messages to all agents
│   └── NNN-from-timestamp.md
├── <session_id>/        # Per-session inbox
│   └── NNN-from-timestamp.md
└── lead/                # Project lead inbox
    └── NNN-from-timestamp.md
```

## Message Format

```markdown
---
from: <session_id>
to: <session_id|"lead"|"owner"|"all">
type: info|question|decision|blocker|context-request|handoff
priority: low|medium|high|urgent
timestamp: 2026-03-14T12:30:00Z
subject: Short summary
---

Message body. Markdown supported.
```

## Types

| Type | When to Use |
|------|-------------|
| `info` | Status update, FYI |
| `question` | Need a decision or clarification |
| `decision` | Recording a decision made |
| `blocker` | Blocked, need help |
| `context-request` | Need context/information from another session |
| `handoff` | Passing work to another agent |

## Agent Map

See `agent-map.md` in this directory for the current active agent roster, file ownership, and coordination rules. **Read this before starting work.**

## Conventions

- File naming: `NNN-<from>-<HHMMSS>.md` (NNN = zero-padded sequence)
- Check inbox at boot and periodically
- Delete messages after processing (or move to .agentrc/inbox/archive/)
- `broadcast/` messages are read-only — don't delete, all agents read them
- `lead` inbox goes to the Project Lead session
- `owner` messages get surfaced to the human
