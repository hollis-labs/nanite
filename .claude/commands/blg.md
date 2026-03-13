Run the blg skill to quick-capture a backlog item to Volon. Takes an argument describing the idea or future work.

Launch an Agent tool (subagent_type: general-purpose, model: haiku) that:
1. Parses the input for title, description, tags, priority, and project
2. Creates the backlog item via mcp__volon__volon_backlog_capture
3. Returns only the confirmation

The item to capture: $ARGUMENTS

If no arguments provided, ask the user what to capture.

The agent must return ONLY:
✓ BLG-ID: Title
Tags: tags | Priority: X | Project: project

Display the agent's response directly. No additional commentary.