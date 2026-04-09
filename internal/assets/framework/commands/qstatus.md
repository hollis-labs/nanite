Run the qstatus skill via a sub-agent. Launch an Agent tool (subagent_type: general-purpose, model: haiku) that checks: (1) active sprints and task counts by status using Engine MCP tools, (2) service health via curl to Engine :8085, Conduit :8080, Hadron :8095, (3) any tasks in "doing" status. The agent must return ONLY this compact format:

=== QSTATUS ===
Services: Engine [UP/DOWN] | Conduit [UP/DOWN] | Hadron [UP/DOWN]

Active Sprints:
  <sprint-code>: <todo>t/<doing>d/<done>✓ of <total>
  ...

In Progress:
  <task-id> <title>
  ...or "None"
===============

Display the agent's response directly. No additional commentary.