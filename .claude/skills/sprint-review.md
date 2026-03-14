# sprint-review

Interactive sprint review — present each task with per-task decisions (approve, defer, carry-over, discuss) using structured dialogs, then execute batch transitions.

## When to use

- When the user asks to review a sprint
- Before closing a sprint (replaces wall-of-text /sprint-end flow)
- When deciding which tasks to carry forward vs close

## Usage

`/sprint-review <sprint_id> [--project <project_id>]`

**sprint_id**: The Volon sprint ID to review
**--project**: Project ID (default: from agentrc.yaml)

## Instructions

### 1. Gather sprint data

- Use `volon_sprint_get` with the sprint ID to get sprint metadata
- Use `volon_tasks_list` filtered by sprint_id to get all tasks
- Determine project_id from the argument, agentrc.yaml, or the sprint's project
- Group tasks by status: done, doing, todo, blocked, paused
- Count totals for the summary header

### 2. Show sprint summary header

Print a brief overview before starting the interactive review:

```
=== SPRINT REVIEW ===
Sprint: <sprint_id>
Project: <project_id>
Tasks: <done>/<total> complete | <doing> in progress | <blocked> blocked

Reviewing <N> tasks that need decisions...
```

Only tasks that are NOT already "done" or "archived" need review. Tasks already done are listed in the summary but skip the interactive step.

### 3. Present tasks for review — interactive dialog

For each task needing a decision, use `AskUserQuestion`. Present up to **4 tasks per call** (one question per task, single-select with action options).

**Question format per task:**
- **header**: Task priority (e.g., "A-pri" or "B-pri") — max 12 chars
- **question**: `[TASK-ID] <title> (status: <current_status>) — what should we do?`
- **options**:
  1. **Approve as done** — "Mark complete and transition to done" (only if task is meaningfully complete)
  2. **Carry over** — "Keep in next sprint with current status"
  3. **Defer to backlog** — "Remove from sprint, return to unassigned backlog"
  4. **Discuss** — "Flag for discussion before deciding"
- **multiSelect**: false (one decision per task)

**Use previews** when the task has a description or acceptance criteria worth showing — put the task description + acceptance criteria in the preview field so the user can see context without leaving the dialog.

**Pagination**: If more than 4 tasks need review, present them in batches of 4 questions per `AskUserQuestion` call. Accumulate all decisions before executing.

### 4. Handle "discuss" selections

For any task the user selects "Discuss":
- Print the full task details (description, acceptance criteria, pointers, dependencies)
- Ask a follow-up question with the same options minus "Discuss" (replace with "Skip for now")
- Record the final decision

### 5. Handle "Other" free-text responses

If the user selects "Other" and types custom text:
- Parse intent — look for keywords: "done", "close", "defer", "move", "archive", "delete"
- If unclear, ask a clarifying follow-up
- Record the decision

### 6. Execute batch transitions

After all decisions are collected, execute in this order:

1. **Approve as done**: `volon_task_transition` to "done" for each
2. **Carry over**: No transition needed — note these for the next sprint assignment
3. **Defer to backlog**: If task has a sprint_id, note it for removal (use `volon_task_update` to clear sprint if supported, otherwise just note it)
4. **Skipped/discussed**: No action, just report

Handle partial failures — if a transition fails, log the error and continue with remaining tasks.

### 7. Show results summary

```
=== REVIEW COMPLETE ===
Sprint: <sprint_id>

APPROVED (done):
  - [TASK-ID] <title>
  - [TASK-ID] <title>

CARRY OVER (next sprint):
  - [TASK-ID] <title>

DEFERRED (backlog):
  - [TASK-ID] <title>

FLAGGED (discuss):
  - [TASK-ID] <title> — <user's note if any>

ALREADY DONE (no action needed):
  - [TASK-ID] <title>

Totals: <N> approved, <N> carried, <N> deferred, <N> flagged
Failed transitions: <N or "none">

Next: Run /sprint-end to commit and close, or /sprint-retro for velocity analysis.
=== END REVIEW ===
```

## Invariants

- Never transition a task without the user's explicit selection
- Always show the summary header before starting interactive review
- Always show results summary after execution
- Handle partial failures gracefully — don't abort on one failed transition
- Tasks already "done" are shown in summary but not presented for review
- Respect the 4-question-per-call limit for AskUserQuestion
- Always include task ID and title in questions for traceability

$ARGUMENTS
