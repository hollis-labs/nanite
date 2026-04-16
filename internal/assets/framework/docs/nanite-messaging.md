# Nanite Messaging (Agent ↔ Agent, Agent ↔ User)

Nanite provides a session-scoped messaging system that lets agents communicate with each other and with the human user during a chat session. Formerly called "A2A" — renamed to `messaging` in Phase 3 S7. See `docs/messaging.md` in the nanite source tree for the full reference (channels, kinds, envelope coupling, service-layer auth).

## Addressing

Messages are addressed to a `(session_id, agent_id)` pair. Agent IDs come in three forms:

- **DB UUID** — assigned when an agent is created via the API.
- **`file-<slug>`** — deterministic ID for file-based agents discovered from `.nanite/config.yaml`.
- **`"user"`** — reserved sentinel for the human user in a session.

The `"user"` value is reserved: you cannot create an agent with slug `"user"`, and you cannot create or rely on an agent with ID `"user"` — that ID is reserved for the human user sentinel.

Unknown `from_agent_id`s are **auto-registered** on first send with `kind='external'` (or `'cli'` when invoked via CLI). Callers don't have to pre-register.

## Channel / Kind / EnvelopeType

Every message carries three orthogonal axes (introduced in S7):

| Axis | Values | Purpose |
|---|---|---|
| **Channel** | `chat`, `inbox`, `alert` | Transport bucket. `chat` = synchronous in-session, `inbox` = async polled work, `alert` = one-off agent-triggered (toast + inbox entry). |
| **Kind** | `request`, `reply`, `notification`, `handoff` | Wire type. Defaults to `notification` if unspecified. |
| **EnvelopeType** | `message-request`, `message-reply`, `message-notification`, `message-handoff`, or any S5 shape | UI content shape. Decoupled from kind — the envelope registry owns these. |

Channel selection is policy, not enforced by code. Default to `chat` for in-session exchanges, `inbox` for work the recipient will pick up later, `alert` for one-shot "report ready" notifications.

## CLI

```bash
# Send a message to an agent.
nanite message send --session <sid> --to file-backend --subject "question" --body "what file handles auth?"

# Send a message to the user.
nanite message send --session <sid> --to user --body "I finished the refactor."

# Read an agent's inbox.
nanite message inbox --session <sid> --agent file-backend

# Read the user's inbox (notifications to the human).
nanite message inbox --session <sid> --agent user

# Get a full thread.
nanite message thread <thread-id>

# Mark a message read.
nanite message ack --session <sid> --agent file-backend <message-id>

# Mark a message resolved.
nanite message resolve --session <sid> --agent file-backend <message-id>

# Get the last N messages in a session (both sides).
nanite message catch-up --session <sid> --last 20
```

All subcommands accept `--db <path>` immediately after `message` to target a non-default database.

## Handoff

Handoff transfers a session's primary agent from one to another. Useful for session resumption, role switching, or context-full bail-outs.

```bash
# Agent requests handoff.
nanite message handoff request --session <sid> --from file-backend --to file-frontend --requested-by departing

# User approves (this counts as user approval in CLI contexts).
nanite message handoff approve <handoff-id>

# Reject.
nanite message handoff reject --reason "not now" <handoff-id>
```

Approval is a single atomic transaction: the session's primary agent flips, the handoff row is marked complete, and any other pending handoffs for the same session are auto-rejected as superseded.

An orphan session with no current primary can also be claimed via `handoff request --to <agent>` with an empty `--from` — `from_agent_id` is optional for this reason.

## HTTP API

```
POST   /api/messaging/send
GET    /api/messaging/inbox?session_id=X&agent_id=Y[&status=Z][&channel=W][&kind=V]
GET    /api/messaging/threads/{threadId}
PUT    /api/messaging/{id}/ack
PUT    /api/messaging/{id}/resolve
GET    /api/messaging/unread?session_id=X&agent_id=Y
GET    /api/messaging/recent?session_id=X&limit=N

POST   /api/handoffs
POST   /api/handoffs/{id}/approve
POST   /api/handoffs/{id}/reject
```

Handoff routes live under `/api/handoffs/*` rather than `/api/messaging/handoffs/*` — they cross into `session_handoffs` + `session_agents`, not just the message store. Validation errors return HTTP 400; internal errors return HTTP 500.

## MCP tools

Six messaging tools are exposed to LLMs:

- `nanite_message_send`
- `nanite_message_inbox`
- `nanite_message_thread`
- `nanite_message_ack`
- `nanite_message_resolve`
- `nanite_message_catch_up`

`nanite_message_send` accepts an optional `reply_to` argument to continue an existing thread, and `channel` / `kind` to select axes.

Handoff is not exposed at the MCP layer — it's user-approval gated and runs through the UI or CLI. Agents that want to propose a handoff surface it via a message, not a direct call.

## Push notifications

Every successful send fires a `message_received` SSE event on the recipient's session stream (via the `NotificationSink` bridge into `StreamManager`). Agents subscribed to their session's SSE stream receive messages without polling.

Every send / ack / resolve also writes rows to `session_events` for replay and context-broker consumption.

## Storage

- `a2a_messages` — every message, with `(from_session_id, from_agent_id, to_session_id, to_agent_id)` addressing columns, plus `channel`, `kind`, `payload_json`. The table name is kept under its legacy `a2a_` prefix as a BLG chore (rename tracked separately).
- `session_handoffs` — audit trail for handoff requests (`pending | approved | rejected | completed`).
- `session_agents` — session-to-agent binding. The `is_primary` column is the authoritative primary-agent flag; handoff approval updates it in the same transaction that marks the handoff row completed.
- `agent_profiles` — extended in S7 with `kind`, `capabilities_json`, `limits_json`, `model_strategy` for registry needs.
- `session_events` — S7 adds `message_sent`, `message_received`, `message_acked`, `message_resolved` rows.

## Validation

`SendMessage` rejects:

- Empty addresses (`from_session_id`, `from_agent_id`, `to_session_id`, `to_agent_id`, `body`)
- Unknown agent IDs (checked against the store + discovered file agents, unless auto-register applies)
- Attempts to use `"user"` as a real agent slug or ID

Validation errors return via a sentinel error — API callers map to HTTP 400; internal errors map to 500.

The caller's `from_agent_id` is trusted in the current single-user desktop MVP. Multi-tenant impersonation checks are a documented follow-up (see Phase 3 S7 G-6.x findings).

## See also

- `docs/messaging.md` — full subsystem reference (service-layer auth, thread participant filter, channel usage policy, migration history)
- `docs/messaging-upgrade-path.md` — compatibility notes from the `a2a → messaging` rename
- `docs/nanite-planner.md` — when to use plans (structured sub-agent work) vs messaging (direct communication)
