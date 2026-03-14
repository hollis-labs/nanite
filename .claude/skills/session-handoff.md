# session-handoff

Interactive session handoff — select what to capture and where to deliver it, then package and send the handoff document.

## Usage

`/session-handoff [target_session_id]`

**target_session_id** (optional): The session that will pick up this work. If omitted, asks interactively.

## Instructions

### 1. Gather session state

- Read `.agentrc/bootstrap.md` for iteration context
- Query Volon for tasks in "doing" status (`volon_tasks_list project_id="mentat" status="doing"`)
- Query Volon for tasks completed recently (`volon_tasks_list project_id="mentat" status="done"`)
- Check `git status` for uncommitted changes
- Check `git log --oneline -10` for recent commits
- Check `adr/` for recently modified ADRs
- Check `.agentrc/inbox/` for unprocessed messages

### 2. Present content selection — interactive dialog

**Question 1: What to capture** (multiSelect)
- **header**: "Content"
- **question**: `What should we include in the handoff?`
- **options**:
  1. **Decisions made** — "ADRs, architectural choices, user preferences from this session"
  2. **Tasks touched** — "Status updates, in-progress work, what's done and what remains"
  3. **Open questions** — "Unresolved items, blockers, things that need attention"
  4. **Gotchas** — "Pitfalls, discoveries, non-obvious context that would be lost"
- **multiSelect**: true

**Question 2: Destination**
- **header**: "Destination"
- **question**: `Where should the handoff go?`
- **options**:
  1. **Next session (generic)** — "Write to broadcast inbox for any agent to pick up"
  2. **Specific agent** — "Send to a named agent session"
  3. **Cortex only** — "Persist to Cortex for long-term reference, no inbox delivery"
  4. **All of the above** — "Broadcast + Cortex for maximum coverage"
- **multiSelect**: false

### 3. Handle "Specific agent" selection

If the user selects "Specific agent":
- List active sessions from `.agentrc/inbox/` directories
- Present as a follow-up `AskUserQuestion` with available targets

### 4. Build the handoff document

Based on selected content types, assemble the handoff:

```markdown
---
from: <current_session_id>
to: <target or "next">
type: handoff
timestamp: <ISO timestamp>
subject: Session Handoff — <1-line summary>
---

## Session Summary
- Session ID: <id>
- Tasks completed: <list>
- Tasks in progress: <list>

<If "Decisions made" selected>
## Decisions Made
<ADRs, architectural choices, user preferences>
</If>

<If "Tasks touched" selected>
## Task State
<For each task: what was done, what remains>
</If>

<If "Open questions" selected>
## Open Questions
<Unresolved items that need attention>
</If>

<If "Gotchas" selected>
## Context & Gotchas
<Pitfalls, discoveries, non-obvious relationships>
</If>

## Recommended Next Steps
1. <highest priority>
2. <second priority>
```

### 5. Deliver the handoff

Based on selected destination:
- **Next session / Specific agent**: Write to `.agentrc/inbox/<target>/handoff-<timestamp>.md` and send via `/send-message`
- **Cortex only**: Use `context_write` with namespace "handoffs"
- **All**: Do both inbox delivery and Cortex write

### 6. Show confirmation

```
=== HANDOFF COMPLETE ===
Content: <selected types>
Destination: <where it went>
File: <path if written to inbox>
Cortex: <key if written to Cortex>

In-progress tasks noted: <N>
Open questions flagged: <N>
=== END HANDOFF ===
```

## When to Use

- Before ending a session with in-progress work
- When handing off to a parallel agent
- When context is complex and would be lost between sessions
- When the user says "wrap up" or "hand off"

## Invariants

- Always ask what to capture — don't assume everything is relevant
- Always confirm destination before writing
- Update Volon task notes for any in-progress tasks
- Never overwrite existing handoff files — use timestamps for uniqueness

$ARGUMENTS
