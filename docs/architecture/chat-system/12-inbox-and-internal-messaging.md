# 12 · Inbox & Internal Messaging

> **Scope:** the email-style internal messaging system. Distinct from [01 Chat Stream Messaging](01-chat-stream-messaging.md) (the user↔assistant turn stream). This is the durable, addressable message system: user inbox, agent inboxes, agent-to-agent messages, status updates, handoff messages.
>
> **Related:** [05 External Agent Execution](05-external-agent-execution.md) (CLI/PTY agents emit messages back via MCP), [03 SSE](03-sse-envelope-and-interaction-protocol.md) (`message_received`).

## Purpose

A durable, queryable mailbox per `(session, agent)` tuple for everything that isn't the live chat stream: agent-to-user notifications, agent-to-agent help requests, directives, status updates, handoff packets.

## Key files

**Backend:**
- `internal/messaging/interface.go` — `Service` interface
- `internal/messaging/service.go` — implementation
- `internal/messaging/handoff.go` — handoff message helpers
- `internal/store/migrations/018_rename_a2a_messages.sql` — table renamed from `a2a_messages` → `agent_messages`

**Frontend:**
- `ui/src/components/messaging/InboxContent.tsx:41` — inbox UI (user/agent tab split)
- `ui/src/lib/api.ts:1573, 1651` — `getMessagingInbox`, `getAgentMessageUnreadCount`
- `ui/src/lib/types.ts:688` — message-type enum

## Storage model

`agent_messages` table. Each row is addressable by `(session_id, agent_id)`. The same agent in two sessions has two inboxes — addresses are session-scoped.

Status enum: `unread` / `read` / `acknowledged` / `resolved`.

Type enum (FE `ui/src/lib/types.ts:688`):

| Type | Use |
|---|---|
| `message` | general communication |
| `help_request` | agent asks another agent / user for help |
| `directive` | instruction to act |
| `status_update` | progress / state-change report |
| `handoff` | structured handoff packet (continuity) |

## Service API

`internal/messaging/interface.go`:

| Method | Purpose |
|---|---|
| `Send` | new message |
| `Get` | fetch by ID |
| `Inbox` | list for `(session, agent)` |
| `Thread` | thread by parent message ID |
| `Recent` | recent across destinations |
| `Ack` | flip status to `acknowledged` |
| `Resolve` | flip status to `resolved` |
| `UnreadCount` | badge support |

## Message flows

### User inbox

User has an inbox surface in the FE (tab in `InboxContent.tsx`). Agents post `message` / `status_update` / `help_request` rows addressed to the user; FE polls / subscribes via `getAgentMessageUnreadCount` for badge count, `getMessagingInbox` for content.

### Agent inboxes

Each agent in a given session has its own inbox. Useful for:
- Subagent results posted back to the parent agent.
- Cross-agent help requests within a session (multi-agent coordination).
- Status updates from a long-running subagent.

### Agent-to-agent instant messaging

`messaging.Service.Send` is synchronous: persists row + emits `session_events`. Recipient's harness can poll their inbox each turn or react to a `message_received` SSE.

### Async / inbox-style

The default. Messages persist in `agent_messages` durably. A receiving agent picks them up at next turn (or via an on-receive hook if the harness wires one).

### Bi-directional CLI/PTY messaging

PARTIAL.

- **CLI/PTY agent → nanite:** YES via the `.mcp.json` injected into their sandbox dir ([05](05-external-agent-execution.md)). The CLI agent's MCP client can call `nanite_message_send` — that goes through nanite's MCP server and persists a message row.
- **nanite → in-flight CLI/PTY agent:** NO inbound channel during a CLI turn. Claude Code controls its own tool loop; nanite cannot inject mid-turn. Messages from nanite to the CLI agent will be visible to it on its *next* turn (when it polls / when the harness reads its inbox before assembling the next turn).

### SSE for CLI/PTY-spawned sessions

PTY observability events `pty_turn_start` / `pty_turn_complete` / `pty_turn_failed` are written to `session_events`. The PTY adapter parses stream-json `assistant`/`result` events into `provider.StreamEvent` which propagates as normal SSE deltas.

**Tool calls inside Claude's own loop are NOT individually surfaced** — the no-silent-drop guard turns "only tool_use, no delta" into a single `EventError`. See [05 gaps](05-external-agent-execution.md#current-gaps).

## Logic gates

- **Address scope is `(session_id, agent_id)`.** Same agent in two sessions = two inboxes. Cross-session message would require mediator.
- **Send is synchronous + persistent.** No fire-and-forget queue; row exists or send failed.
- **Status transitions** are linear: `unread → read → acknowledged → resolved`. Skipping levels is not enforced but not idiomatic.

## Capabilities matrix — "What can each agent type emit?"

| Agent type | Can emit notification? | Can emit instant-message? | Can emit state-change? |
|---|---|---|---|
| Nanite Harness chat agent | YES via `nanite_message_send` | YES same | YES same |
| Inline-parallel tool exec | YES (within turn) | YES | YES |
| Async PTY/CLI (Claude Code) | YES via `.mcp.json` → `nanite_message_send` (depends on MCP client invoking it) | YES same | YES same |
| Chat-Session Parallel | YES (independent harness) | YES | YES |
| Background (`internal/background/pty.go`) | NO — no MCP, no chat surface, no SSE | NO | NO |

## Current gaps

- **G-CLI-PROGRESS-VISIBILITY** — CLI/PTY agents *can* emit messages back via MCP (status_update during tool steps), but this is opt-in per-prompt. The transport-level SSE enrichment (per-line tool-name `status` event) isn't wired. See [05](05-external-agent-execution.md), [gaps.md](gaps.md#g-pty-no-tool-events).
- **G-INBOX-NO-PUSH** — `message_received` SSE is in the event taxonomy, but the FE consumption path for it isn't verified for the new external-agent flows ([03 gap](03-sse-envelope-and-interaction-protocol.md#current-gaps)).

## Test surface

- `messaging.Service` round-trip: send → inbox → ack → resolve.
- Threading: parent message + 2 children → assert `Thread` returns ordered.
- CLI/PTY back-channel: spawn a PTY agent, exercise its MCP client calling `nanite_message_send`, assert row appears in parent's inbox.
- Cross-session isolation: agent X in session A and session B; send to (A, X), assert (B, X) inbox is empty.
- Status state machine: send → fetch unread; mark read; ack; resolve. Each transition queryable.
