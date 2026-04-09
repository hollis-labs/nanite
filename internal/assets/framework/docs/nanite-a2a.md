# Nanite A2A (Agent-to-Agent Messaging)

Nanite provides a session-scoped messaging system that lets agents communicate with each other and with the human user during a chat session.

## Addressing

Messages are addressed to a `(session_id, agent_id)` pair. Agent IDs come in three forms:

- **DB UUID** — A UUID string assigned when an agent is created via the API.
- **`file-<slug>`** — A deterministic ID for file-based agents discovered from `.nanite/config.yaml`.
- **`"user"`** — The reserved sentinel for addressing the human user in a session.

The `"user"` value is reserved: you cannot create an agent with slug `"user"`, and you cannot create or rely on an agent with ID `"user"` — that ID is reserved for the human user sentinel used to address the human participant in a session.

## CLI

```bash
# Send a message to an agent.
nanite a2a send --session <sid> --to file-backend --subject "question" --body "what file handles auth?"

# Send a message to the user.
nanite a2a send --session <sid> --to user --body "I finished the refactor."

# Read an agent's inbox.
nanite a2a inbox --session <sid> --agent file-backend

# Read the user's inbox (notifications to the human).
nanite a2a inbox --session <sid> --agent user

# Get a full thread.
nanite a2a thread <thread-id>

# Mark a message read.
nanite a2a ack --session <sid> --agent file-backend <message-id>

# Mark a message resolved.
nanite a2a resolve --session <sid> --agent file-backend <message-id>

# Get the last N messages in a session (both sides).
nanite a2a catch-up --session <sid> --last 20
```

All subcommands accept `--db <path>` immediately after `a2a` to target a non-default database.

## Handoff

Handoff transfers a session's primary agent from one to another. Useful for session resumption, role switching, or context-full bail-outs.

```bash
# Agent requests handoff.
nanite a2a handoff request --session <sid> --from file-backend --to file-frontend --requested-by departing

# User approves (this counts as user approval in CLI contexts).
nanite a2a handoff approve <handoff-id>

# Reject.
nanite a2a handoff reject --reason "not now" <handoff-id>
```

Approval is a single atomic transaction: the session's primary agent flips, the handoff row is marked complete, and any other pending handoffs for the same session are auto-rejected as superseded.

An orphan session with no current primary can also be claimed via `handoff request --to <agent>` with an empty `--from` — the `from_agent_id` is optional for this reason.

## HTTP API

The same operations are exposed over HTTP:

- `POST /api/a2a/messages` — send a message
- `GET /api/a2a/inbox?session_id=X&agent_id=Y[&status=Z]` — read an inbox
- `GET /api/a2a/threads/{threadId}` — fetch a thread
- `PUT /api/a2a/messages/{id}/ack` — ack (body: `{session_id, agent_id}`)
- `PUT /api/a2a/messages/{id}/resolve` — resolve (body: `{session_id, agent_id}`)
- `GET /api/a2a/unread?session_id=X&agent_id=Y` — unread count
- `POST /api/a2a/handoffs` — request a handoff
- `POST /api/a2a/handoffs/{id}/approve` — approve
- `POST /api/a2a/handoffs/{id}/reject` — reject
- `GET /api/a2a/recent?session_id=X&limit=N` — catch-up for a session

Validation errors return HTTP 400; internal errors return HTTP 500.

## MCP tools

All of the above are also exposed as MCP tools for agents running inside Nanite or external CLI agents (Claude Code, Gemini CLI, etc.) connected via MCP:

- `nanite_a2a_send`
- `nanite_a2a_inbox`
- `nanite_a2a_thread`
- `nanite_a2a_ack`
- `nanite_a2a_resolve`
- `nanite_a2a_catch_up`
- `nanite_a2a_handoff_request`
- `nanite_a2a_handoff_approve`
- `nanite_a2a_handoff_reject`

`nanite_a2a_send` accepts an optional `reply_to` argument to continue an existing thread.

Streaming subscription (`nanite_a2a_subscribe`) is a follow-up — it requires streaming support in mcp-go or a custom server-side handler. See the spec for details.

## Validation

`SendMessage` rejects:

- Empty addresses (`from_session_id`, `from_agent_id`, `to_session_id`, `to_agent_id`, `body`)
- Unknown agent IDs (checked against the store + discovered file agents)
- Attempts to use `"user"` as a real agent slug or ID

Validation errors are returned via the `a2a.ErrValidation` sentinel so API callers can map them to HTTP 400 (`errors.Is(err, a2a.ErrValidation)` → `400`, else → `500`).

The caller's `from_agent_id` is **trusted** in the current single-user desktop MVP. Multi-tenant impersonation checks are a documented follow-up.

## Storage

- `a2a_messages` — every message, with `(from_session_id, from_agent_id, to_session_id, to_agent_id)` addressing columns.
- `session_handoffs` — audit trail for handoff requests (`pending | approved | rejected | completed`).
- `session_agents` — session-to-agent binding. The `is_primary` column is the authoritative primary-agent flag; handoff approval updates it in the same transaction that marks the handoff row completed.

No new binding table was introduced for handoff — the existing `session_agents` junction table carries the state.
