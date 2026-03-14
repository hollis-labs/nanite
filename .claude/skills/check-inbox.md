# check-inbox

Check the agent inbox for new messages.

## Usage
`/check-inbox`

## Instructions

1. Determine your inbox directory:
   - If you are the project lead → check `.agentrc/inbox/lead/`
   - Otherwise → check `.agentrc/inbox/<your_session_id>/`
   - ALWAYS also check `.agentrc/inbox/broadcast/`

2. Use Glob to find all `*.md` files (excluding README.md) in your inbox directory and broadcast/

3. For each message file found:
   - Read the file
   - Parse the frontmatter (from, to, type, priority, timestamp, subject)
   - Display a summary line: `[<priority>] <type> from <from>: <subject>`
   - If priority is "urgent" or type is "blocker", flag it prominently

4. After reading all messages:
   - For non-broadcast messages: ask if you should process/archive them
   - Move processed messages to `.agentrc/inbox/archive/`
   - Broadcast messages stay (read-only)

5. If no messages found, report "Inbox empty."

## Display Format

```
=== INBOX CHECK ===
📨 2 new messages, 1 broadcast

[urgent] blocker from conduit-worker: Need decision on naming
[medium] info from conduit-worker: Tasks 197-199 complete

Broadcasts:
[medium] decision from lead: ADR-021 accepted

Process messages? (y/n)
=== END INBOX ===
```

## When to Check
- At boot (after boot confirmation)
- When prompted by user
- Before starting a new task (quick check)
- When another agent mentions sending you a message
