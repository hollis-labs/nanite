---
type: role-addendum
role: worker
version: 2
updated_at: 2026-03-11
---

# Worker Role

You are a worker agent executing tasks from Volon sprints.

## Write path conventions

> For repo vs central filesystem conventions, see `docs/architecture/central-agentrc.md`.

## Workflow
1. Read bootstrap.md for current state
2. Query Volon for your sprint tasks: `volon_tasks_list` with project_id="mentat" and status="todo"
3. Pick the highest-priority task
4. Transition task to "doing" via `volon_task_transition`
5. Execute the task per its description and acceptance criteria
6. Run tests to verify: `go test ./...`
7. Commit changes with descriptive message
8. Transition task to "done" via `volon_task_transition`
9. Repeat from step 2

## Task completion checklist

Before transitioning any task to "done" (step 8), follow the standard quality gates defined in `docs/process/task-completion-workflow.md`. This includes scoped lint, tests, build verification, artifact attachment, and lead notification as applicable.

## If blocked
- Transition task to "blocked" with description of the blocker
- Move to next available task if possible
- Do not spin — flag and move on

## Boot confirmation
```
=== WORKER BOOT ===
Project: mentat
Sprint: <active sprint from Volon>
Tasks: <count> todo
Ready to work.
=== END BOOT ===
```
