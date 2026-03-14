# session-handoff

Package the current session's state into a handoff document for the next agent session, ensuring context continuity.

## Usage
`/session-handoff [target_session_id]`

**target_session_id** (optional): The session that will pick up this work. If omitted, creates a generic handoff.

## Instructions

1. Gather current session state:
   - Read `.agentrc/bootstrap.md` for iteration context
   - Query Volon for tasks in "doing" status (`volon_tasks_list project_id="mentat" status="doing"`)
   - Query Volon for tasks completed this session (`volon_tasks_list project_id="mentat" status="done"` — filter by recent)
   - Check git status for uncommitted changes
   - Check git log for commits made this session

2. Summarize decisions made:
   - List any ADRs created or updated (check `adr/` for recent modifications)
   - List any architectural decisions discussed but not yet recorded
   - List any open questions or unresolved blockers

3. Identify in-progress work:
   - Tasks currently in "doing" — what's done, what remains
   - Files modified but not committed
   - Any worktrees that are still active

4. Capture context that would be lost:
   - Key findings from research or debugging
   - Relationships discovered between components
   - Gotchas or pitfalls encountered
   - User preferences expressed during the session

5. Write the handoff document:

   If `target_session_id` is provided:
   - Write to `.agentrc/inbox/<target_session_id>/handoff-<timestamp>.md`
   - Also send via `/send-message <target_session_id> handoff Session handoff — <summary>`

   If no target:
   - Write to `.agentrc/inbox/broadcast/handoff-<timestamp>.md`

6. Update Volon task notes for any in-progress tasks with current state.

## Handoff Document Format

```markdown
---
from: <current_session_id>
to: <target_session_id or "next">
type: handoff
timestamp: <ISO timestamp>
subject: Session Handoff — <1-line summary>
---

## Session Summary
- Session ID: <id>
- Duration: <approximate>
- Tasks completed: <list>
- Tasks in progress: <list>

## Completed Work
<For each completed task: what was done, key files changed>

## In-Progress Work
<For each doing task: what's done, what remains, current approach>

## Uncommitted Changes
<git status summary, what the changes are for>

## Decisions Made
<ADRs, architectural choices, user preferences>

## Open Questions
<Unresolved items that need attention>

## Context & Gotchas
<Important discoveries, pitfalls, relationships>

## Recommended Next Steps
1. <highest priority>
2. <second priority>
3. <third priority>
```

## When to Use
- Before ending a session with in-progress work
- When handing off to a parallel agent
- When context is complex and would be lost between sessions
- When the user says "wrap up" or "hand off"
