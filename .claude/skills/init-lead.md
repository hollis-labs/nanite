# init-lead

Initialize this session as the A2A Project Lead. Sets up inbox, agent map, and broadcasts lead status.

## Usage
`/init-lead`

## Instructions

1. **Set session identity:**
   - Your session ID is `lead`
   - Your inbox is `.agentrc/inbox/lead/`

2. **Ensure inbox structure exists:**
   ```bash
   mkdir -p .agentrc/inbox/{lead,broadcast,archive}
   ```

3. **Read or create agent map:**
   - Check if `.agentrc/inbox/agent-map.md` exists
   - If yes, read it and update the lead entry to "active"
   - If no, create it with the standard template (lead as only active session)

4. **Check inbox for messages:**
   - Read all *.md files in `.agentrc/inbox/lead/` (excluding README)
   - Read all *.md files in `.agentrc/inbox/broadcast/`
   - Summarize any unread messages

5. **Check for active agents:**
   - List directories in `.agentrc/inbox/` (each dir = a session ID)
   - Check Volon for tasks in "doing" status to see who's working

6. **Broadcast lead status:**
   Write to `.agentrc/inbox/broadcast/`:
   ```markdown
   ---
   from: lead
   to: all
   type: info
   priority: medium
   timestamp: <now>
   subject: Lead session active
   ---
   Project Lead is online. Send status updates to .agentrc/inbox/lead/.
   ```

7. **Emit confirmation:**
   ```
   === LEAD INITIALIZED ===
   Inbox: .agentrc/inbox/lead/
   Active agents: <list or "none">
   Unread messages: <count>
   Agent map: .agentrc/inbox/agent-map.md
   === END ===
   ```

## When to Use
- At the start of any orchestration session
- After boot if you are the meta-agent in Project Lead role
- When resuming a session where you were previously the lead
