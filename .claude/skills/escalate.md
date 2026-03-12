# Escalate

Convert a blocker into an explicit repair task so the system continues operating without hidden failure.

## When to use

- When a task is blocked and cannot proceed
- When a blueprint or automated operation fails
- When an architectural decision is needed before work can continue

## Procedure

1. **Identify active task.** Check Volon for the current "doing" task, or accept explicit `--task <ID>`.

2. **Transition task to blocked.** Use `volon_task_transition` to set status to "blocked". Use `volon_task_update` to add the blocker reason to the description.

3. **Create repair task.** Use `volon_task_create`:
   - Title: `Repair: <short blocker summary>`
   - Priority: A (repair tasks always high priority)
   - Tags: repair, escalation
   - Description: link to parent task, blocker reason, evidence, specific next actions
   - Acceptance criteria: parent task unblocked and can proceed

4. **Write ADR if needed.** If the blocker involves an architectural or process decision:
   - Create `adr/ADR-NNN-<slug>.md`
   - Status: Proposed
   - Skip if the blocker is purely operational (missing binary, network issue)

5. **Update bootstrap.** Note the blocked task and repair task in `.agentrc/bootstrap.md`.

6. **Emit signal.** `**[TASK-ID] blocked** — <one-line blocker summary>`

## Output

- Volon: blocked task + new repair task
- ADR (if architectural decision needed)
- Console: BLOCKED signal

## Invariants

- Never silently swallow a blocker — always create a repair task
- Repair tasks always have priority A
- Blocker reason must be specific, not vague
- ADRs only for architectural/process decisions, not operational issues
