# /pause-task

Pause the current active task, capturing work state into a PCC capsule for deterministic resumption.

## Usage

```
/pause-task [--task <TASK-ID>] [--mode soft|restart|compact]
```

- `--task`: explicit task ID (default: auto-detect from Volon `doing` tasks)
- `--mode`: pause mode — `soft` (resume anytime), `restart` (new session), `compact` (compact context first). Default: `soft`

## Steps

1. **Identify active task.** Query Volon via `volon_tasks_list` for tasks with status `doing`. If explicit `--task` provided, use that. If no active task found, print error and stop.

2. **Transition task.** Use `volon_task_transition` to set status to `paused`.

3. **Write Task PCC capsule.** Create or update `.agentrc/pcc/tasks/<task_id>.md` with:
   ```markdown
   ---
   task_id: <TASK-ID>
   paused_at: YYYY-MM-DDTHH:MM:SSZ
   confidence: high|medium|low
   ---

   ## Goal
   <task title and acceptance criteria>

   ## Current plan / hypothesis
   <what approach is being taken>

   ## Key files touched
   - <file1>
   - <file2>

   ## Commands run + outcomes
   - `<command>` -> <result summary>

   ## Next 1-3 actions
   1. <specific next step>
   2. <specific next step>

   ## Open questions / blockers
   - <question or blocker, if any>
   ```
   **Cap:** max 400 words. Trim prose, not structure.

4. **Update bootstrap.md.** Set frontmatter fields:
   - `paused_task_id: <task_id>`
   - `paused_at: YYYY-MM-DDTHH:MM:SSZ`
   - `resume_hint: <one-line description of next action>`

5. **Output resume instructions.** Print based on mode:
   - `soft`: "Task paused. Resume anytime with `/resume-task`."
   - `restart`: "Task paused. Start a new session and run `/resume-task`."
   - `compact`: "Task paused. Run `/compact` then `/resume-task`."

## Invariants

- Never pause without writing a PCC capsule
- PCC capsule must have "Next 1-3 actions" — never leave blank
- Capsule max 400 words — trim prose not structure
- Bootstrap must reflect paused state after this command runs
- Task must be transitioned to `paused` in Volon before writing capsule

$ARGUMENTS
