# /resume-task

Resume a paused or pending task by loading its PCC capsule and executing the next actions.

## Usage

```
/resume-task [--task <TASK-ID>]
```

- `--task`: explicit task ID (default: auto-detect from bootstrap `paused_task_id`, then most recent paused/doing task from Volon, then highest-priority todo)

## Steps

1. **Identify target task.** Priority order:
   - `paused_task_id` from `.agentrc/bootstrap.md` frontmatter
   - Most recent task with status `paused` or `doing` from Volon (`volon_tasks_list`)
   - Highest-priority task with status `todo`
   - If explicit `--task` provided, use that instead
   - If no task found, print "No resumable task found" and stop.

2. **Re-ground from repo artifacts.** Read (in order):
   - `agentrc.yaml` — system config
   - `.agentrc/pcc/global/` — L0 Global PCC (if files exist)
   - `.agentrc/pcc/tasks/<task_id>.md` — L2 Task PCC capsule (if exists)
   - Volon task details via `volon_task_get` — authoritative task state

3. **Load Task PCC capsule.** If `.agentrc/pcc/tasks/<task_id>.md` exists:
   - Read `paused_at` timestamp
   - Extract "Next 1-3 actions" list
   - Check `confidence` level
   - Check "Open questions / blockers" for unresolved items
   - If capsule doesn't exist, proceed with Volon task data alone, note "No PCC capsule"

4. **Preflight checklist.** Verify before proceeding:
   - [ ] No unresolved blockers in task or capsule
   - [ ] Confidence is not `low` (if low, warn but allow proceed)
   - [ ] Task exists in Volon and is readable
   - **Result:** `pass` -> proceed | `warn` -> proceed with notes | `block` -> stop and explain

5. **Continue execution.**
   - Transition task to `doing` via `volon_task_transition`
   - Clear `paused_task_id` from bootstrap (set to null)
   - Execute "Next 1-3 actions" from capsule (or task description)
   - Apply normal loop rules: small steps, update task after each step

6. **End condition.** Stop after:
   - 1-3 actions completed
   - Task reaches `done` or `blocked` state
   - First concrete step completes if resuming from `todo`

## Invariants

- Always read the PCC capsule if it exists — never skip it
- Always transition task to `doing` before executing actions
- Always clear `paused_task_id` from bootstrap on resume
- If preflight blocks, do not execute, explain why
- Never resume a `done` task — print warning and stop

$ARGUMENTS
