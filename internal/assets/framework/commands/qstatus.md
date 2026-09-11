Run the qstatus skill via a sub-agent. Launch an Agent tool (subagent_type: general-purpose, model: haiku) that checks: (1) active sprints and task counts by status using Torque MCP tools (`mcp__torque__torque_sprint_list`, `mcp__torque__torque_task_list`), (2) service health via curl to Torque :8085, Tesseract :8089, Hadron :8095, using configured deployment addresses when overridden, (3) any tasks in "doing" status. The agent must return ONLY this compact format:

=== QSTATUS ===
Services: Torque [UP/DOWN] | Tesseract [UP/DOWN] | Hadron [UP/DOWN]

Active Sprints:
  <sprint-code>: <todo>t/<doing>d/<done>✓ of <total>
  ...

In Progress:
  <task-id> <title>
  ...or "None"
===============

Display the agent's response directly. No additional commentary.
