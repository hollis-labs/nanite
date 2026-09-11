Run the blg skill to quick-capture a backlog item to Torque. Takes an argument describing the idea or future work.

Launch an Agent tool (subagent_type: general-purpose, model: haiku) that:
1. Parses the input for title, description, tags, priority, and project
2. Creates the backlog task via mcp__torque__torque_task_create (with `backlog` tag, status=backlog)
3. Returns only the confirmation

The item to capture: $ARGUMENTS

If no arguments provided, ask the user what to capture.

The agent must return ONLY:
✓ CW-ID: Title
Tags: tags | Priority: X | Project: project_id

Display the agent's response directly. No additional commentary.
