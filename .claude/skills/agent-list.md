# agent-list

List all active agent sessions by checking inbox directories and recent messages.

## Usage
`/agent-list`

## Instructions

1. List all directories in `.agentrc/inbox/` (excluding `broadcast/`, `lead/`, `archive/`, and `README.md`)
2. Each directory name is a session ID
3. For each session, check for recent messages (files modified in last 2 hours)
4. Also check Volon for tasks in "doing" status across all projects to see what's active
5. Check `.agentrc/inbox/lead/` for recent status reports

Display:
```
=== ACTIVE AGENTS ===
lead (this session)
  Role: Project Lead
  Current: <task or "orchestrating">

conduit-agent
  Last message: <timestamp> — <subject>
  Inbox: <N> unread messages
  Volon: <task in doing status or "idle">

tools-agent
  Last message: <timestamp> — <subject>
  Inbox: <N> unread messages
  Volon: <task in doing status or "idle">

Broadcast: <N> messages
=== END ===
```
