# Backlog Capture (:blg)

Quick-capture a backlog item to Clockwork. Runs via sub-agent to keep main context clean.

## When to use

- When the user types `:blg` or `/blg` with an idea or future work item
- When something comes up in conversation that should be tracked but isn't urgent
- Example: `/blg Investigate PostgreSQL migration path for Tesseract`

## IMPORTANT: Run in Sub-Agent

This skill MUST be executed via the Agent tool (subagent) to keep the main context clean.

## Procedure

Parse the user's input for the backlog title and any details. If the input includes tags, priority, or project hints, extract those too.

Launch an Agent with this prompt (fill in from user input):

```
You are a backlog capture agent for Clockwork Manifold. Create a well-structured backlog task.

## Input from user:
{USER_INPUT}

## Steps:

1. Parse the input:
   - Extract a clear, concise title (imperative mood, e.g., "Investigate X", "Add Y", "Fix Z")
   - Extract or infer a description/body with context on why this matters
   - Extract tags if mentioned, or infer from content (e.g., architecture, optimization, infrastructure, agent, gui, api). Always include the `backlog` tag.
   - Default priority: B (unless user specifies)
   - Resolve project_id: if the user named a project, look it up via mcp__clockwork__clockwork_project_list. Otherwise default to PRJ-20260417-0001 (Agent Ops) when run from the agent-workspaces context.

2. Create the backlog task:
   - Use mcp__clockwork__clockwork_task_create with title, body, priority, project_id, tags. Set status to `backlog` (or the project's equivalent triage status).

3. Return ONLY this format:
   ✓ {CW-id}: {title}
   Tags: {tags} | Priority: {priority} | Project: {project_id}
```

## Output

Display the sub-agent's confirmation. No additional commentary needed.

## Invariants

- ALWAYS run via sub-agent
- Default project: PRJ-20260417-0001 (Agent Ops) when run from agent-workspaces; otherwise the project the user names
- Default priority: B
- Always include the `backlog` tag
- Title should be actionable (imperative mood)
- Body should include enough context for someone to understand the item later
- Keep it fast — this is a quick-capture tool, not a planning exercise
