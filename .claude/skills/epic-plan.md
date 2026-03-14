# epic-plan

Interactive epic planning — show an epic's sprints with execution strategy options, dependency previews, and inline actions for reordering and dispatch.

## Usage

`/epic-plan <epic_id> [--project <project_id>]`

**epic_id**: The Volon epic ID to plan
**--project**: Project ID (default: "mentat" from agentrc.yaml)

## Instructions

### 1. Gather epic data

- Use `volon_epic_get` with the epic ID
- Use `volon_sprints_list` filtered by epic_id to get all sprints
- For each sprint, use `volon_tasks_list` filtered by sprint_id to get task counts and statuses
- Identify dependencies between sprints (tasks with `depends_on` pointing to other sprints' tasks)

### 2. Show epic summary header

```
=== EPIC PLAN ===
Epic: <epic_id>
Title: <title>
Project: <project_id>
Sprints: <N> total | <done>/<N> complete
Tasks: <done>/<total> across all sprints

Sprint breakdown:
  <sprint_code>: <done>/<total> tasks (<status>)
  ...
```

### 3. Present execution strategy — interactive dialog

Use `AskUserQuestion` to let the user choose the execution approach.

**Question 1: Execution strategy**
- **header**: "Strategy"
- **question**: `How should we execute the remaining sprints?`
- **options**:
  1. **Sequential** — "Execute sprints one at a time in order"
  2. **Parallel pairs** — "Run independent sprints simultaneously where dependencies allow"
  3. **Minimal commitment** — "Start one sprint, re-evaluate before committing to more"
  4. **Custom order** — "I'll specify the execution order"
- **multiSelect**: false
- **preview**: For each option, show the sprint sequence with dependency chains and estimated timeline

### 4. Present sprint-level decisions

After strategy is selected, present remaining sprints for activation. Up to **4 sprints per `AskUserQuestion` call**.

**Question format per sprint:**
- **header**: Sprint status (e.g., "Planned", "Active")
- **question**: `[<sprint_code>] <N> tasks — activate now?`
- **options**:
  1. **Activate** — "Start this sprint now"
  2. **Queue next** — "Activate after current sprint completes"
  3. **Hold** — "Keep planned, don't schedule yet"
  4. **Discuss** — "Review tasks before deciding"
- **multiSelect**: false
- **preview**: Show sprint's task list with priorities and dependencies

### 5. Handle "Discuss" and "Custom order"

For **Discuss**: Print full sprint details (all tasks, dependencies, blockers), then re-ask with options minus Discuss.

For **Custom order**: Present all remaining sprints as a multiSelect question — user selects them in the order they want. Parse selection order as execution order.

### 6. Execute sprint activations

After all decisions collected:

1. **Activate**: Update sprint status to "active" if Volon supports it, otherwise note for manual activation
2. **Queue next**: Record the ordering for sequential execution
3. **Hold**: No action

### 7. Show execution plan summary

```
=== EXECUTION PLAN ===
Epic: <epic_id>
Strategy: <selected strategy>

ACTIVE NOW:
  - <sprint_code>: <N> tasks

QUEUED (in order):
  1. <sprint_code>: <N> tasks (after <dependency>)
  2. <sprint_code>: <N> tasks

ON HOLD:
  - <sprint_code>: <N> tasks

Dependencies noted:
  - <sprint_A> must complete before <sprint_B> (TASK-X depends on TASK-Y)

Next: Pick a task from the active sprint, or run /sprint-review when ready.
=== END PLAN ===
```

## Invariants

- Never activate sprints without user selection
- Always show dependency chains when presenting strategy options
- Respect task dependencies — warn if activating a sprint whose dependencies aren't met
- Works with epics of 2–8 sprints
- Always present the execution summary after decisions

$ARGUMENTS
