# workflow-session-end-capture

Auto-persist context when ending a session. Captures decisions, progress, and state so the next session can pick up seamlessly.

## Usage
`/workflow-session-end-capture`

No arguments — captures everything from the current session automatically.

## Instructions

Execute these steps in order:

### Step 1: Capture In-Progress Work
1. Query Volon for tasks in "doing" status for this project
2. For each in-progress task, update its notes with current state:
   - What's been done so far
   - What remains
   - Current approach/strategy
   - Any blockers or open questions
3. Do NOT transition tasks — leave them in "doing" for the next session

### Step 2: Capture Uncommitted Changes
1. Run `git status` to identify modified files
2. Run `git diff --stat` for a summary
3. If there are significant uncommitted changes:
   - Create a WIP commit with message "wip: <what was being worked on>"
   - Or document the changes in a task note if committing isn't appropriate

### Step 3: Update Bootstrap
1. Read current `.agentrc/bootstrap.md`
2. Update the "Current state" section with:
   - What was accomplished this session
   - What's in progress
   - Any new blockers or decisions
3. Write the updated bootstrap

### Step 4: Sync to Cortex
1. Write a session summary to Cortex:
   - Namespace: `app/<project_id>`
   - Key: `session-<date>-<session_id>`
   - Content: session summary with tasks, decisions, state

### Step 5: Create Session Handoff
1. Use `/session-handoff` to create a handoff document
2. This goes to `.agentrc/inbox/broadcast/` for any agent to pick up

### Step 6: Update Memory
1. Check if any new facts should be saved to memory:
   - New project conventions discovered
   - User feedback received
   - Architecture decisions made
   - Reference information learned
2. Write/update memory files as needed

### Step 7: Final Report

```
=== SESSION END CAPTURE ===
Session: <session_id>
Project: <project_id>
Date: <date>

WORK CAPTURED:
  Tasks in progress: <N> (notes updated)
  Uncommitted changes: <committed as WIP | documented | none>
  Bootstrap: updated
  Cortex: session record written
  Handoff: created

DECISIONS RECORDED:
  - <decision 1>
  - <decision 2>

MEMORY UPDATES:
  - <memory file updated/created>

NEXT SESSION SHOULD:
  1. <highest priority>
  2. <second priority>
  3. <third priority>
=== END CAPTURE ===
```

## When to Use
- Before ending any work session
- When the user says "wrap up", "I'm done for now", "end session"
- Before a long break
- When switching to a different project
