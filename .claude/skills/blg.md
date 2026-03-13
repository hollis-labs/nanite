# Backlog Capture (:blg)

Quick-capture a backlog item to Volon. Runs via sub-agent to keep main context clean.

## When to use

- When the user types `:blg` or `/blg` with an idea or future work item
- When something comes up in conversation that should be tracked but isn't urgent
- Example: `/blg Investigate PostgreSQL migration path for Cortex`

## IMPORTANT: Run in Sub-Agent

This skill MUST be executed via the Agent tool (subagent) to keep the main context clean.

## Procedure

Parse the user's input for the backlog title and any details. If the input includes tags, priority, or project hints, extract those too.

Launch an Agent with this prompt (fill in from user input):

```
You are a backlog capture agent for Project Tiamat / Fragments Engine. Create a well-structured backlog item.

## Input from user:
{USER_INPUT}

## Steps:

1. Parse the input:
   - Extract a clear, concise title (imperative mood, e.g., "Investigate X", "Add Y", "Fix Z")
   - Extract or infer a description/body with context on why this matters
   - Extract tags if mentioned, or infer from content (e.g., architecture, optimization, infrastructure, agent, gui, api)
   - Default priority: B (unless user specifies)
   - Default project: volon (unless user specifies another project)

2. Create the backlog item:
   - Use mcp__volon__volon_backlog_capture with title, body, priority, project_id, tags

3. Return ONLY this format:
   ✓ BLG-{id}: {title}
   Tags: {tags} | Priority: {priority} | Project: {project}
```

## Output

Display the sub-agent's confirmation. No additional commentary needed.

## Invariants

- ALWAYS run via sub-agent
- Default project: volon
- Default priority: B
- Title should be actionable (imperative mood)
- Body should include enough context for someone to understand the item later
- Keep it fast — this is a quick-capture tool, not a planning exercise
