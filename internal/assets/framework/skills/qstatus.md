# Quick Status (:qstatus)

Compact status snapshot of sprints, tasks, and services. Returns a structured summary without polluting the caller's context.

## When to use

- When the user types `:qstatus` or asks "how's things going?"
- When you need a quick status check without pulling raw API data into your context
- Periodic check-ins during long sessions

## IMPORTANT: Run in Sub-Agent

This skill MUST be executed via the Agent tool (subagent) to keep the main context clean. The sub-agent runs the queries, digests them, and returns ONLY the compact summary. The raw API responses stay in the sub-agent's context, not yours.

## Procedure

Launch an Agent with this prompt:

```
You are a status reporter. Run the following queries and return ONLY a compact summary table. Do not include raw API output.

1. Check active sprints:
   - Use mcp__torque__torque_sprint_list with the resolved project_id, limit=5 (resolve it with torque_project_list; do not assume one)
   - For each active sprint, use mcp__torque__torque_task_list with parent_id=<sprint_id> to get task counts by status

2. Check service health:
   - curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8085/v1/tasks --max-time 2 (Torque)
   - curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8089/v1/health/readiness --max-time 2 (Tesseract; use the configured deployment address if overridden)
   - curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8095/ --max-time 2 (Hadron)

3. Check for any tasks in "doing" status (stuck work):
   - Use mcp__torque__torque_task_list with status=doing, limit=5

Return this EXACT format and nothing else:

=== QSTATUS ===
Services: Torque [UP/DOWN] | Tesseract [UP/DOWN] | Hadron [UP/DOWN]

Active Sprints:
  <sprint-code>: <todo>t/<doing>d/<done>✓ of <total> [active/planned]
  ...

In Progress:
  <task-id> <title> (sprint: <code>)
  ...or "None"
===============
```

## Output

Display the sub-agent's response directly. No additional commentary needed.

## Invariants

- ALWAYS run via sub-agent — never pull raw task/sprint lists into main context
- Read-only — no state changes
- Compact output — the whole point is minimal context footprint
- Timeout: 10s max for the full check
