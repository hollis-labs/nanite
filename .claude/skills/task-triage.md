# task-triage

Batch classify and prioritize unassigned tasks. Analyzes tasks without sprints or with low priority, suggests classifications, and optionally applies them.

## Usage
`/task-triage [--project <project_id>] [--apply]`

**--project**: Project ID to triage (default: all projects)
**--apply**: Apply suggested changes to Volon (default: dry run)

## Instructions

1. **Fetch unassigned tasks**:
   - Use `volon_tasks_list` with status="todo" for the target project(s)
   - Also check `volon_backlog_list` for uncaptured items
   - Filter to tasks that have no sprint_id or are in a generic/catch-all sprint

2. **Classify each task** by analyzing its title and description:

   **Priority classification** (if not already set or set to default):
   - **A (Critical)**: Blockers, security issues, data loss risks, broken builds
   - **B (Important)**: Feature work, planned improvements, technical debt
   - **C (Nice-to-have)**: Polish, minor improvements, nice-to-haves

   **Category classification**:
   - `bug` — something is broken
   - `feature` — new capability
   - `chore` — maintenance, cleanup, config
   - `docs` — documentation
   - `refactor` — code improvement without behavior change
   - `test` — test coverage
   - `infra` — infrastructure, CI/CD, tooling

   **Effort estimation**:
   - `small` — <1 hour, single file change
   - `medium` — 1-4 hours, multiple files
   - `large` — 4+ hours, architectural

   **Sprint suggestion**:
   - Match task to the most relevant existing sprint based on epic alignment
   - If no matching sprint, suggest creating one or adding to backlog

3. **Check for duplicates**:
   - Compare task titles across the project
   - Flag potential duplicates or overlapping tasks

4. **Check for dependencies**:
   - Does this task depend on another task that isn't done?
   - Does another task depend on this one?

5. **Generate triage report**:

```
=== TASK TRIAGE ===
Project: <project_id>
Tasks triaged: <N>

PRIORITY CHANGES:
  TASK-<id>: <title>
    Current: <priority> -> Suggested: <new_priority>
    Reason: <why>

CATEGORY ASSIGNMENTS:
  TASK-<id>: <title> -> <category> (<effort>)

SPRINT SUGGESTIONS:
  TASK-<id>: <title> -> <sprint_id> (<sprint name>)
    Reason: <why this sprint>

DUPLICATES FOUND:
  TASK-<id> and TASK-<id>: <overlap description>
    Suggest: merge into <which one>

DEPENDENCY WARNINGS:
  TASK-<id> depends on TASK-<id> (status: <status>)

<If --apply>
CHANGES APPLIED:
  - TASK-<id>: priority updated to <new>
  - TASK-<id>: moved to sprint <sprint_id>
</If>

SUMMARY:
  <N> priority changes suggested
  <N> sprint assignments suggested
  <N> duplicates found
  <N> dependency warnings
=== END TRIAGE ===
```

## When to Use
- When the backlog is growing and needs organization
- Before sprint planning
- When new tasks have been bulk-created
- Periodically to keep task hygiene
