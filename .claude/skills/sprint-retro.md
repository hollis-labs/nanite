# sprint-retro

Analyze a completed sprint with velocity metrics, insights, and improvement suggestions.

## Usage
`/sprint-retro <sprint_id> [--project <project_id>]`

**sprint_id**: The Volon sprint ID to analyze
**--project**: Project ID (default: from agentrc.yaml)

## Examples
- `/sprint-retro SPR-20260314-TOOLS-S2-SKILLS-AND-COMMANDS`
- `/sprint-retro SPR-20260314-PREFLIGHT-P1 --project mentat`

## Instructions

1. **Fetch sprint data**:
   - Use `volon_sprint_get` with the sprint ID
   - Use `volon_tasks_list` filtered by sprint_id to get all tasks
   - For each task, note: status, priority, title, created_at, updated_at

2. **Calculate velocity metrics**:
   - Total tasks in sprint
   - Tasks completed (done)
   - Tasks still open (todo/doing)
   - Tasks blocked
   - Tasks paused
   - Completion rate: `done / total * 100`
   - Estimate effort distribution: count by priority (A/B/C) and by effort tags if available

3. **Analyze execution pattern**:
   - Order of task completion (which were done first?)
   - Were quick wins prioritized? (effort-small tasks done early)
   - Were any tasks blocked for extended periods?
   - Did any tasks get added mid-sprint?

4. **Check for scope creep**:
   - Compare task count at sprint creation vs current
   - Identify tasks added after sprint start
   - Note if sprint scope expanded

5. **Cross-reference with git history**:
   - Use `git log --since=<sprint_start> --until=<sprint_end>` to see commits
   - Map commits to tasks where possible (look for TASK-ID in commit messages)
   - Count commits per task for effort distribution

6. **Generate insights**:
   - What went well? (completed on time, clean execution)
   - What was challenging? (blocked tasks, scope changes)
   - Patterns: were similar types of tasks consistently faster/slower?
   - Dependencies: did cross-project dependencies cause delays?

7. **Suggest improvements**:
   - Based on patterns, what should change in next sprint?
   - Are there process improvements to capture?
   - Should task sizing be adjusted?

## Display Format

```
=== SPRINT RETROSPECTIVE ===
Sprint: <sprint_id>
Project: <project_id>
Status: <sprint status>

VELOCITY:
  Total tasks: <N>
  Completed: <N> (<percent>%)
  In progress: <N>
  Blocked: <N>
  Paused: <N>

  By priority: A=<N> B=<N> C=<N>

EXECUTION TIMELINE:
  1. <task_id> — <title> (completed <date>)
  2. <task_id> — <title> (completed <date>)
  ...
  <remaining>: <task_id> — <title> (status: <status>)

SCOPE:
  Original tasks: <N>
  Added mid-sprint: <N>
  Scope change: <+/- N> (<percent>% change)

INSIGHTS:
  + <what went well>
  + <what went well>
  - <what was challenging>
  - <what was challenging>

IMPROVEMENTS:
  1. <suggestion>
  2. <suggestion>
  3. <suggestion>

CARRY-FORWARD:
  <tasks to move to next sprint, if any>
=== END RETRO ===
```

## When to Use
- When a sprint is completed or nearly completed
- During planning for the next sprint (review the last one first)
- When the user asks for a sprint summary or retrospective
- At the end of an iteration
