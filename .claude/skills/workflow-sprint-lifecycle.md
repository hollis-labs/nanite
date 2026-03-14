# workflow-sprint-lifecycle

End-to-end sprint lifecycle: plan, create, execute, close, and retrospective.

## Usage
`/workflow-sprint-lifecycle <phase> [args]`

**phase**: Which lifecycle phase to execute
- `plan <epic_id>` — Plan the next sprint for an epic
- `create <epic_id> <sprint_name>` — Create the sprint in Volon with tasks
- `execute` — Execute the current sprint (pick and work tasks)
- `close <sprint_id>` — Close a sprint, carry forward incomplete tasks
- `retro <sprint_id>` — Run retrospective (delegates to /sprint-retro)
- `full <epic_id>` — Run the full lifecycle from plan through retro

## Instructions

### Phase: Plan
1. Use `volon_epic_get` to understand the epic scope
2. List existing sprints for the epic (`volon_sprints_list`)
3. Check what's already done vs remaining
4. Analyze remaining tasks by priority and effort
5. Propose a sprint scope:
   - Sprint name/code
   - Which tasks to include (aim for 8-15 tasks)
   - Suggested order of execution
   - Dependencies between tasks
   - Estimated effort distribution
6. Present the plan and wait for approval before creating

### Phase: Create
1. Create the sprint in Volon (`volon_sprint_create`)
2. Create tasks for the sprint (`volon_task_create` for each)
3. Set priorities based on the plan
4. Confirm creation with task count

### Phase: Execute
1. Check which sprint is active (has "doing" tasks or is the latest planned sprint)
2. Pick the highest-priority todo task
3. Claim it (`volon_task_transition` to "doing")
4. Execute the task
5. Run `/code-review` on changes if applicable
6. Mark done (`volon_task_transition` to "done")
7. Repeat until sprint is complete or user stops

### Phase: Close
1. Fetch all tasks for the sprint
2. Identify incomplete tasks (todo, doing, blocked)
3. For incomplete tasks, decide:
   - Carry forward to next sprint? (create in next sprint)
   - Move to backlog? (`volon_backlog_capture`)
   - Archive? (no longer relevant)
4. Update sprint status
5. Report what was carried forward

### Phase: Retro
1. Delegate to `/sprint-retro <sprint_id>`
2. Save key insights to Cortex for future reference

### Phase: Full
Execute all phases in sequence: plan -> create -> execute -> close -> retro

```
=== SPRINT LIFECYCLE: <phase> ===
Epic: <epic_id>
Sprint: <sprint_id or "planning">
Phase: <current phase>

<phase-specific output>

Next phase: <what comes next>
=== END ===
```

## When to Use
- When starting a new sprint
- When the current sprint is wrapping up
- When you need a structured approach to sprint management
- When the user says "let's plan the next sprint" or "close this sprint"
