# Escalate (:escalate)

Convert a blocker into an explicit repair task so the system continues operating without hidden failure.

## When to use

- When a task is blocked and cannot proceed
- When a blueprint or automated operation fails
- When an architectural decision is needed before work can continue

## Procedure

1. **Identify active task.** Check Clockwork for the current "doing" task (`mcp__clockwork__clockwork_task_list status=doing`), or accept explicit `--task <CW-ID>`.

2. **Transition task to blocked.** Use `mcp__clockwork__clockwork_task_transition` to set status to "blocked". Use `mcp__clockwork__clockwork_task_update` to add the blocker reason to the description, or `mcp__clockwork__clockwork_comment_add` for a threaded note.

3. **Create repair task.** Use `mcp__clockwork__clockwork_task_create`:
   - Title: `Repair: <short blocker summary>`
   - Priority: A (repair tasks always high priority)
   - Tags: repair, escalation
   - Description: link to parent task, blocker reason, evidence, specific next actions
   - Acceptance criteria: parent task unblocked and can proceed

4. **Write ADR if needed.** If the blocker requires a design or architecture decision (e.g., "should we change the API contract?" or "which approach do we take?"), invoke the `:adr` skill.
   - Skip if the blocker is purely operational (missing binary, network issue, config error).

5. **Emit signal.** `**[CW-ID] blocked** — <one-line blocker summary>`

## Output

- Clockwork: blocked task + new repair task
- ADR (if architectural decision needed)
- Console: BLOCKED signal

## Invariants

- Never silently swallow a blocker — always create a repair task
- Repair tasks always have priority A
- Blocker reason must be specific, not vague
- ADRs only for design/architecture decisions, not operational issues
