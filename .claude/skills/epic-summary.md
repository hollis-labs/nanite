# epic-summary

Generate a full epic status report with progress metrics, sprint breakdown, and trajectory projection.

## Usage
`/epic-summary <epic_id>`

**epic_id**: The Volon epic ID to summarize

## Examples
- `/epic-summary EPIC-20260314-11561`
- `/epic-summary EPIC-20260314-65311`

## Instructions

1. **Fetch epic details**:
   - Use `volon_epic_get` with the epic ID
   - Get title, description, status, created_at

2. **Fetch all sprints for this epic**:
   - Use `volon_sprints_list` and filter by epic_id
   - For each sprint, get its status and task count

3. **Fetch all tasks across all sprints**:
   - Use `volon_tasks_list` filtered by sprint_id for each sprint
   - Aggregate task statuses across the entire epic

4. **Calculate metrics**:
   - Total tasks across all sprints
   - Tasks by status: todo, doing, done, blocked, paused
   - Overall completion percentage
   - Per-sprint completion percentage
   - Blocked task ratio

5. **Determine trajectory**:
   - Based on completion rate and time elapsed, estimate:
     - On track / at risk / behind
   - Count remaining work (todo + doing tasks)
   - Identify bottlenecks (blocked tasks, sprints with low completion)

6. **Check for cross-project dependencies**:
   - Read task descriptions for references to other projects
   - Flag any external blockers

7. **Generate the summary**:

```
=== EPIC SUMMARY ===
Epic: <epic_id>
Title: <title>
Status: <status>
Created: <date>

PROGRESS: <done>/<total> tasks (<percent>%)
[=========>          ] <visual bar>

SPRINTS:
  1. <sprint_code> — <done>/<total> (<percent>%) [<status>]
     Active tasks: <list of doing tasks>
     Blocked: <list if any>

  2. <sprint_code> — <done>/<total> (<percent>%) [<status>]
     ...

BY STATUS:
  Done:    <N> ████████
  Doing:   <N> ███
  Todo:    <N> █████
  Blocked: <N> █
  Paused:  <N>

TRAJECTORY: <On Track | At Risk | Behind>
  Completion rate: <tasks/day or tasks/sprint>
  Remaining: <N> tasks
  <If behind> Bottleneck: <sprint or task causing delay>

BLOCKERS:
  - TASK-<id>: <title> — <blocker description>

CROSS-PROJECT:
  - <dependency on other project if any>

KEY DECISIONS:
  - <ADRs related to this epic>
=== END SUMMARY ===
```

## When to Use
- During planning meetings or status checks
- When the user asks "how is <epic> going?"
- Before sprint planning for the next sprint in an epic
- For standup context on a specific epic
