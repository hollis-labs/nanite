# Messaging subsystem

Nanite's first-class agent-to-agent and agent-to-user message primitive. Introduced in Phase 3 Session 7 as a rebuild of the former `a2a` subsystem — new Go surface + HTTP / MCP / CLI names, and (migration 018) the `a2a_messages` table was renamed to `agent_messages`. The `session_handoffs` table name was already neutral and is unchanged.

Package: `internal/messaging/` (+ `internal/subagent/` for the inline subagent spawn flow).

## Vocabulary

Three orthogonal axes describe any message. The naming distinction is deliberate — earlier drafts conflated them and created confusion.

| Axis | Values | Purpose | Column |
|---|---|---|---|
| **Channel** | `chat`, `inbox`, `alert` | Transport bucket. Controls delivery semantics — `chat` is synchronous in-session traffic, `inbox` is async polled work, `alert` is an agent-triggered one-off ("report ready") delivered as toast + inbox entry. | `agent_messages.channel` |
| **Kind** | `request`, `reply`, `notification`, `handoff` | Wire type. Signals what shape of payload rides on the message and how receivers should react. `notification` is the default when the caller does not specify. | `agent_messages.kind` |
| **EnvelopeType** (S5) | `message-request`, `message-reply`, `message-notification`, `message-handoff`, and many non-messaging shapes like `proposal-card`, `collect-data`, etc. | UI content shape. Lives in `config/envelopes.yaml`. A message whose `kind=request` typically rides an `envelope_type=message-request` shape but this isn't a hard coupling — content shapes are owned by the S5 envelope registry, not the messaging layer. |

Channel is POLICY not code in MVP — the CHECK constraint bounds the set of acceptable values, but what to send on which channel is a convention (not enforced by the application layer). See §Channel-usage-policy below.

## Store interface

Go-side: `messaging.Store`. Nexus-shaped with one adaptation — nanite addresses messages by `(session_id, agent_id)` tuples, whereas Nexus used a single agent id.

```go
type Store interface {
    Send(ctx, SendInput)                                     (*Message, error)
    Get(ctx, msgID string)                                   (*Message, error)
    Inbox(ctx, sessionID, agentID string, filter InboxFilter) ([]Message, error)
    Thread(ctx, threadID string)                             ([]Message, error)
    Recent(ctx, sessionID string, limit int)                 ([]Message, error)
    Ack(ctx, msgID string)                                   error
    Resolve(ctx, msgID string)                               error
    UnreadCount(ctx, sessionID, agentID string)              (int, error)
}
```

The `SQLiteStore` is the only implementation; backed by the `agent_messages` table (renamed from the legacy `a2a_messages` by migration 018; the `agent_` prefix avoids collision with the separate session-chat `messages` table).

## Service layer

`messaging.Service` wraps `Store` and adds:

- **Auth checks.** `Inbox` / `Ack` / `Resolve` verify caller identity against the row's `(to_session_id, to_agent_id)`. Defensive-only for MVP (tool-broker is the real ACL); catches misaddressed calls loudly. Returns `ErrForbidden`.
- **Thread participant filter.** `Thread(threadID, callerSessionID, callerAgentID)` returns only rows where the caller is sender or recipient. Non-participants see empty (no existence leak).
- **Auto-register on first send.** When `FromAgentID` is unknown and `AgentRegistrar` is wired, the service inserts a minimal `agent_profiles` row with `kind='external'` (or `'cli'` via `SendInput.RegisterAs`).
- **Pubsub fan-out.** Existing MCP-streaming subscribers get the new message via `SubscribeSessionAgent`.
- **Notification sink.** A `NotificationSink` hook bridges every successful send to the session SSE stream via `StreamManager.BroadcastSessionStreamEvent` — emits a `message_received` StreamEvent.
- **Session event-log.** Every send / ack / resolve writes rows to `session_events` for replay and context-broker consumption.
- **Handoff operations.** `RequestHandoff` / `ApproveHandoff` / `RejectHandoff` cross into `session_handoffs` + `session_agents` via direct `*sql.DB` (not the Store interface, since handoff transactions span multiple tables).

## Channel usage policy

Channel is an un-enforced convention. For agents composing messages:

| Channel | When to use |
|---|---|
| `chat` | In-session conversational traffic between agents in the same session. Primary ↔ secondary coordination during a turn. Default when unspecified. |
| `inbox` | Async work requests — "handle this when you're free." Agents poll their inbox between turns. Used for user → agent long-running requests and agent → user status updates. |
| `alert` | Agent-triggered one-off notifications ("report ready", "build failed"). Delivered as toast on the recipient plus an inbox row. High-visibility, no reply expected. |

## Auto-register convention

When an unknown `from_agent_id` sends a message:

- Default: stamped as `kind='external'` (e.g. a new plugin-backed agent).
- With `RegisterAs="cli"` (MCP arg `register_as`, HTTP header `X-Nanite-Agent-Kind: cli`, CLI sets this automatically): stamped as `kind='cli'`.

**CLI deterministic-id convention (G-1):** when `nanite message send` is invoked with `--cli` AND `--from` is omitted (or left at the `user` sentinel), the CLI substitutes `cli-<hostname>-<pid>-<start-unix>` as the `from_agent_id`. Same id across every send in the same process; new process → new id. Human users piping into `nanite message send` without `--cli` keep the default `user` sentinel.

## Subagent spawn

`internal/subagent/` implements the inline subagent spawn flow — a primary agent can spawn a short-lived subagent to handle a subtask.

**Lifecycle:** `requested → approved → running → completed | failed | cancelled` (persisted in `subagent_runs`).

**Modes:**
- `sync` — blocking spawn; caller holds until the runner returns. Reply lands in `chat` channel.
- `async` — returns immediately; reply lands in `inbox` channel when done.
- `api` — returns immediately; reply lands in `chat` channel. For trusted callers that don't want to block but do want the reply surfaced synchronously-looking.

**Approval:** all three modes auto-approve in MVP. Interactive-approval envelope (plan §D13) is a follow-up (T9.2).

**Runner:** `subagent.Runner` interface. `EchoRunner` stub ships with S7; real chat-engine-backed runner is T9.1 follow-up.

**Reply delivery:** on completion, `subagent.Service` posts a message via `messaging.Service` addressed from the role to the parent agent. `Kind=reply`. Channel chosen per mode.

## MCP tool surface

| Tool | Purpose |
|---|---|
| `nanite_message_send` | Send a message. |
| `nanite_message_inbox` | Read an agent's inbox (status / channel / kind filters). |
| `nanite_message_thread` | Get all messages in a thread (participant-filtered). |
| `nanite_message_ack` | Mark message as read. |
| `nanite_message_resolve` | Mark message as resolved. |
| `nanite_message_catch_up` | Recent messages in a session (catch-up for newly-arrived agents). |
| `nanite_handoff_request` | Create a pending primary-agent handoff. |
| `nanite_handoff_approve` | Approve a pending handoff (transactional primary-flip). |
| `nanite_handoff_reject` | Reject a pending handoff. |
| `nanite_spawn_subagent` | Spawn an inline subagent. |
| `nanite_subagent_status` | Read a subagent run's current state. |
| `nanite_subagent_cancel` | Cancel an in-flight subagent run. |

## HTTP surface

```
GET    /api/messaging/inbox             — list inbox for (session_id, agent_id)
GET    /api/messaging/threads/{threadId} — participant-filtered thread rows
POST   /api/messaging/send              — send a message
PUT    /api/messaging/{id}/ack          — mark as read
PUT    /api/messaging/{id}/resolve      — mark as resolved
GET    /api/messaging/unread            — unread count
GET    /api/messaging/recent            — recent messages in a session

POST   /api/handoffs                    — create pending handoff
POST   /api/handoffs/{id}/approve       — approve + rebind primary
POST   /api/handoffs/{id}/reject        — reject with reason
```

`/api/messaging/*` is distinct from `/api/messages/*` (the latter is for session-chat messages — a different primitive).

## Resource bounds

- `Recent` limit: default 20, cap 100 (`messaging.MaxRecentLimit`). Prevents LLM-controlled memory DoS.
- `SessionEvents` limit: default 100, cap 500.
- Pubsub subscriber buffer: 16 messages. Slow subscribers are dropped non-blocking.
- Unary MCP handler calls get a 30s timeout wrapper (`messageCallTimeout`).

## Shutdown

`messaging.Service.Close()` drains all pubsub subscribers and clears the subscriber map. Wired into `Container.Shutdown` so in-flight MCP stream subscribers unblock cleanly.

## Future direction

The stream-scoped addressing model (Engine's `docs/unified-messaging-architecture.md`) is the long-term target. See `docs/messaging-upgrade-path.md` for the migration sketch.
