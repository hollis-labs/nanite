# Escalate (:escalate)

Convert a blocker into an explicit repair task so the system continues operating without hidden failure.

## When to use

- When a task is blocked and cannot proceed
- When a blueprint or automated operation fails
- When an architectural decision is needed before work can continue

## Procedure

1. **Identify active task.** Check Engine for the current "doing" task, or accept explicit `--task <ID>`.

2. **Transition task to blocked.** Use `mcp__engine__engine_task_transition` to set status to "blocked". Use `mcp__engine__engine_task_update` to add the blocker reason to the description.

3. **Create repair task.** Use `mcp__engine__engine_task_create`:
   - Title: `Repair: <short blocker summary>`
   - Priority: A (repair tasks always high priority)
   - Tags: repair, escalation
   - Description: link to parent task, blocker reason, evidence, specific next actions
   - Acceptance criteria: parent task unblocked and can proceed

4. **Write ADR if needed.** If the blocker requires a design or architecture decision (e.g., "should we change the API contract?" or "which approach do we take?"), invoke the `:adr` skill.
   - Skip if the blocker is purely operational (missing binary, network issue, config error).

5. **Emit signal.** `**[TASK-ID] blocked** — <one-line blocker summary>`

## Output

- Engine: blocked task + new repair task
- ADR (if architectural decision needed)
- Console: BLOCKED signal

## Invariants

- Never silently swallow a blocker — always create a repair task
- Repair tasks always have priority A
- Blocker reason must be specific, not vague
- ADRs only for design/architecture decisions, not operational issues
