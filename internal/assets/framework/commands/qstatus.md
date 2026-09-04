Run the qstatus skill via a sub-agent. Launch an Agent tool (subagent_type: general-purpose, model: haiku) that checks: (1) active sprints and task counts by status using Clockwork MCP tools (`mcp__clockwork__clockwork_sprint_list`, `mcp__clockwork__clockwork_task_list`), (2) service health via curl to Clockwork :8085, Tesseract :8089, Hadron :8095, using configured deployment addresses when overridden, (3) any tasks in "doing" status. The agent must return ONLY this compact format:

=== QSTATUS ===
Services: Clockwork [UP/DOWN] | Tesseract [UP/DOWN] | Hadron [UP/DOWN]

Active Sprints:
  <sprint-code>: <todo>t/<doing>d/<done>✓ of <total>
  ...

In Progress:
  <task-id> <title>
  ...or "None"
===============

Display the agent's response directly. No additional commentary.
