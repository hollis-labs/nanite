# send-message

Send a message to another agent session or the project lead via the A2A inbox.

## Usage
`/send-message <to> <type> <subject> — <body>`

**to**: Session ID, "lead", "owner", or "all" (broadcast)
**type**: info, question, decision, blocker, context-request, handoff
**subject**: Short summary (one line)
**body**: Everything after the em dash

## Examples
- `/send-message lead info Tasks 197-199 complete — All three task flow enforcement tasks are done and tested.`
- `/send-message lead blocker Need decision on naming — Should Hadron blueprint "Task" be renamed to "Step" or "Cmd"?`
- `/send-message all decision ADR-021 accepted — Quality gates will use golangci-lint + Lefthook + Biome.`
- `/send-message conduit-agent context-request Conduit MVP scope — What tasks are in Demo-Ready sprint? Need current priorities.`

## Instructions

1. Parse the arguments: `<to>`, `<type>`, `<subject>` (before em dash), `<body>` (after em dash)
2. Determine the target directory:
   - If `to` is "all" → `.agentrc/inbox/broadcast/`
   - If `to` is "lead" → `.agentrc/inbox/lead/`
   - If `to` is "owner" → surface to user directly AND write to `.agentrc/inbox/lead/`
   - Otherwise → `.agentrc/inbox/<to>/` (create dir if needed)
3. Count existing files in the target directory to get the next sequence number
4. Generate filename: `NNN-<my_session_id>-<HHMMSS>.md`
5. Write the message file with frontmatter:

```markdown
---
from: <my_session_id>
to: <to>
type: <type>
priority: medium
timestamp: <current ISO timestamp>
subject: <subject>
---

<body>
```

6. Confirm: "Message sent to <to>: <subject>"

**Session ID**: Use a short identifier for this session. If you don't have one assigned, use the boot profile name (e.g., "meta-agent", "conduit-worker").

**Priority escalation**: If type is "blocker", auto-set priority to "urgent". If type is "question", set to "high". Otherwise "medium".
