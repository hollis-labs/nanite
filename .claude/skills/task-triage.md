# task-triage

Interactive batch triage of unassigned tasks — present tasks in prioritized batches with per-task options (assign, defer, archive, group) using structured dialogs, then execute bulk operations.

## Usage

`/task-triage [--project <project_id>]`

**--project**: Project ID to triage (default: "mentat" from agentrc.yaml)

## Instructions

### 1. Gather unassigned tasks

- Use `volon_tasks_list` with project_id and status="todo" to get all todo tasks
- Filter to tasks with no sprint_id or in a catch-all sprint
- Also check `volon_backlog_list` for uncaptured items
- Sort by: priority (A first), then age (oldest first)
- Fetch full details (`volon_task_get`) for each to get descriptions and tags

### 2. Show triage summary header

```
=== TASK TRIAGE ===
Project: <project_id>
Unassigned tasks: <N>
By priority: A=<N> B=<N> C=<N> unset=<N>

Triaging in batches of 4...
```

### 3. Present tasks for triage — interactive dialog

Present up to **4 tasks per `AskUserQuestion` call** (one question per task, single-select).

**Question format per task:**
- **header**: Priority tag (e.g., "A-pri", "B-pri", "No-pri") — max 12 chars
- **question**: `[TASK-ID] <title> (age: <days> days) — what should we do?`
- **options**:
  1. **Assign to sprint** — "Add to an active or upcoming sprint"
  2. **Defer to backlog** — "Keep as unassigned for future consideration"
  3. **Archive** — "Stale or no longer relevant — archive it"
  4. **Discuss** — "Need more context before deciding"
- **multiSelect**: false
- **preview**: Show task description, tags, and acceptance criteria (if available)

**Pagination**: Present tasks in prioritized batches of 4. After each batch, continue to the next. Accumulate all decisions.

### 4. Handle sprint assignment

For tasks selected as "Assign to sprint":
- Use `volon_sprints_list` to get active/planned sprints for the project
- Present a follow-up `AskUserQuestion` with available sprints as options (up to 4)
- Include a "Create new sprint" option if appropriate
- Record the sprint assignment

### 5. Handle "Discuss" selections

- Print full task details (description, acceptance criteria, dependencies, tags)
- Ask a follow-up with the same options minus "Discuss" (replace with "Skip for now")
- Record the final decision

### 6. Execute batch operations

After all decisions are collected, execute in this order:

1. **Assign to sprint**: Use `volon_task_update` to set sprint_id for each
2. **Defer to backlog**: No action needed — task stays as-is
3. **Archive**: Use `volon_task_transition` → doing → archived (or force if needed)
4. **Skipped**: No action, report only

Handle partial failures — log errors and continue.

### 7. Show results summary

```
=== TRIAGE COMPLETE ===
Project: <project_id>

ASSIGNED TO SPRINTS:
  - [TASK-ID] <title> → <sprint_id>

DEFERRED (backlog):
  - [TASK-ID] <title>

ARCHIVED:
  - [TASK-ID] <title>

FLAGGED (discuss):
  - [TASK-ID] <title>

Totals: <N> assigned, <N> deferred, <N> archived, <N> flagged
Remaining unassigned: <N>
Failed operations: <N or "none">
=== END TRIAGE ===
```

## Invariants

- Never archive or transition without user selection
- Always show summary header before interactive review
- Always show results summary after execution
- Handle 50+ tasks without overwhelming — paginate in batches of 4
- Respect the 4-question-per-call limit
- Always include task ID and title for traceability

$ARGUMENTS
