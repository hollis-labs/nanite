# delegate

Delegate work to another agent via the A2A inbox system. Creates a handoff message with full context for the target agent.

## Usage
`/delegate <session_id> <epic_or_sprint_id> — <context and instructions>`

**session_id**: The target agent's session ID (e.g., "tools-agent", "volon-worker")
**epic_or_sprint_id**: The epic or sprint to assign
**context**: Additional instructions, constraints, priorities

## Examples
- `/delegate tools-agent EPIC-20260314-11418 — Focus on S2 skills sprint. All markdown, zero build risk. Start with session-handoff and task-triage.`
- `/delegate volon-worker SPR-20260314-GUI-POLISH — Fix the 8 GUI bugs. Start with effort-small items.`

## Instructions

1. Parse arguments: `<session_id>`, `<epic_or_sprint_id>`, `<context>`

2. Gather context for the handoff:
   - Read the epic/sprint details from Volon (use `volon_epic_get` or `volon_sprint_get`)
   - List the tasks (use `volon_tasks_list` with sprint_id or filter by epic)
   - Check for dependencies, blocked tasks, related ADRs
   - Read relevant architecture docs if referenced in task pointers

3. Create the target agent's inbox directory:
   ```
   mkdir -p .agentrc/inbox/<session_id>/
   ```

4. Write a handoff message to `.agentrc/inbox/broadcast/`:
   ```markdown
   ---
   from: lead
   to: <session_id>
   type: handoff
   priority: high
   timestamp: <current ISO timestamp>
   subject: <epic/sprint title> — Delegated to <session_id>
   ---

   ## Scope
   <Epic/sprint title and description>

   ## Tasks (priority order)
   <List all tasks with ID, title, priority, status>

   ## Key Context
   <Relevant ADRs, architecture docs, conventions>
   <What the lead and other agents are doing in parallel>
   <Known constraints, what NOT to touch>

   ## Communication
   - Your session ID: <session_id>
   - Project lead inbox: .agentrc/inbox/lead/
   - Use /send-message lead <type> <subject> — <body>
   - Check .agentrc/inbox/<session_id>/ and broadcast/ for messages
   - PING THE OWNER before any rename/move operations

   ## Dependencies
   <Tasks that must complete first, cross-agent coordination>
   ```

5. Write a boot prompt to `.agentrc/inbox/<session_id>/000-boot-prompt.md`:
   ```markdown
   ---
   from: lead
   to: <session_id>
   type: handoff
   priority: urgent
   timestamp: <current ISO timestamp>
   subject: Boot instructions for <session_id>
   ---

   <Full boot prompt text — see /delegate-prompt skill>
   ```

6. Confirm: "Delegated <epic/sprint> to <session_id>. Handoff in broadcast/, boot prompt in inbox."
