# ADR-025: Volon A2A Messaging & Queue Management

## Status: Accepted

## Date: 2026-03-14

## Context

Fragments Engine has a working CLI-based A2A messaging system (file-based inbox in `.agentrc/inbox/`), but the GUI side — Nanite and Volon — has no equivalent. To demonstrate the system effectively and enable real-time human-agent collaboration through the GUI, we need A2A messaging as a Volon-native primitive.

Current Volon comments are flat: `id, entity_type, entity_id, author (string), body, created_at`. No sender typing, no routing, no message state, no read tracking. The existing file-based inbox works for CLI agents but can't drive a GUI experience.

ADR-009 (Unified Messaging Streams) describes the full end-state: streams, participants, scoped message routing. That's the right destination, but shipping it as a monolith blocks progress. We need an incremental path that delivers GUI-based A2A now and evolves toward unified streams later.

Key requirements:
- Comments on tasks serve as the A2A channel for GUI/Runner interactions
- Each comment tagged with sender identity (agent hash, user hash) and message type
- Inbox paradigm: pull all unread messages, respond task-by-task — no infinite polling loops
- "Waiting on response" tracking so tasks aren't mistaken for stalled/stale
- Stuck detection: if awaiting reply exceeds a threshold, escalate
- Type-safe, clean data model — no stringly-typed hacks

## Decision

### Phase 1: Extend Volon comments for routed A2A messaging

Extend the existing comments table rather than building the full streams model. This preserves backward compatibility while adding the routing, typing, and state tracking needed for GUI-based A2A.

#### Schema changes

**Comments table — new columns:**

| Column | Type | Default | Purpose |
|---|---|---|---|
| `from_type` | TEXT NOT NULL | `'user'` | Sender category: `agent`, `user`, `system` |
| `from_id` | TEXT | NULL | Sender identity hash (agent session hash, user ID) |
| `to_id` | TEXT | NULL | Recipient identity (NULL = broadcast to all participants) |
| `message_type` | TEXT NOT NULL | `'comment'` | Classification: `comment`, `directive`, `status_update`, `handoff`, `question`, `decision` |
| `status` | TEXT NOT NULL | `'open'` | Message lifecycle: `open`, `read`, `acknowledged`, `resolved` |
| `read_at` | TEXT | NULL | Timestamp when recipient marked as read |
| `updated_at` | TEXT | NULL | Last state change timestamp |

**Tasks table — new columns:**

| Column | Type | Default | Purpose |
|---|---|---|---|
| `awaiting_reply_from` | TEXT | NULL | Identity of agent/user we're waiting on |
| `awaiting_since` | TEXT | NULL | Timestamp when wait began |

#### New MCP tools

**`volon_message_send`** — Create a typed, routed message on an entity.

| Param | Required | Description |
|---|---|---|
| `entity_type` | yes | `task`, `sprint`, `backlog_item` |
| `entity_id` | yes | Entity ID |
| `body` | yes | Message content (markdown) |
| `from_type` | yes | `agent`, `user`, `system` |
| `from_id` | yes | Sender identity hash |
| `to_id` | no | Recipient identity (omit for broadcast) |
| `message_type` | no | Default `comment`. Options: `comment`, `directive`, `status_update`, `handoff`, `question`, `decision` |

Behavior: Creates the comment record with routing fields populated. If `message_type` is `question` or `directive`, auto-sets `awaiting_reply_from` on the parent task to `to_id` and `awaiting_since` to now (only for `entity_type=task`).

**`volon_inbox_list`** — Get unread messages for a given identity.

| Param | Required | Description |
|---|---|---|
| `agent_id` | yes | Identity hash to check inbox for |
| `entity_type` | no | Filter by entity type |
| `entity_id` | no | Filter to specific entity |
| `status` | no | Filter by status (default: `open`) |
| `limit` | no | Max results (default 25) |

Returns: Messages where `to_id = agent_id` AND `status` matches filter, ordered by `created_at` DESC.

**`volon_message_ack`** — Acknowledge/update a message's status.

| Param | Required | Description |
|---|---|---|
| `message_id` | yes | Comment ID to update |
| `status` | yes | New status: `read`, `acknowledged`, `resolved` |

Behavior: Updates `status` and `read_at`/`updated_at`. If the message was a `question` or `directive` and the new status is `resolved`, clears `awaiting_reply_from` on the parent task.

#### Existing tools — backward compatible

`volon_comment_add` and `volon_comments_list` continue to work unchanged. New columns have defaults, so old callers produce valid records (type=`user`, message_type=`comment`, status=`open`).

### Phase 2: Nanite GUI integration

**Task comment stream**: Render the comment thread on each task as a conversation view. Messages show sender identity, type badge, and read/unread state.

**Inbox view**: New dashboard component — "Tasks waiting on you." Queries `volon_inbox_list` for the current user/agent, groups by task, shows unread count and latest message preview. Clicking a task opens its comment stream.

**Post-as-identity**: When Nanite's chat engine or a user posts a comment, tag it with the session's agent hash or user ID. The identity is determined at the API layer, not by the caller.

### Phase 3: Queue orchestration

**Queue Controller** — A Special Agent profile that:
- Watches the task queue for `todo` tasks in active sprints
- Routes tasks to Execute Agents based on task tags, domain, and agent availability
- Monitors `awaiting_reply_from` timestamps for stuck detection
- Escalates via `volon_message_send` with `message_type=directive` when thresholds are exceeded

**Per-Task Context Agent** — A Special Agent that:
- Holds conversation context for one task
- Bridges human ↔ worker agent via the comment stream
- Maintains the illusion of real-time conversation through inbox pull + response
- Comments are the communication channel; task status tracks the workflow state

**Lead Agent** — The orchestrator that:
- Manages the Queue Controller and Context Agents
- Makes routing decisions when the Queue Controller can't auto-resolve
- Eventually becomes an internal Volon process (not a CLI agent)

**Stuck detection rules:**
- `awaiting_reply_from` set AND `awaiting_since` older than configurable threshold (default: 30 min for agents, 24h for users)
- Queue Controller sends escalation directive to Lead Agent
- Lead Agent can reassign, nudge, or surface to human

### Migration path to ADR-009 (Unified Streams)

This design is intentionally forward-compatible:
- `from_type` + `from_id` maps directly to `StreamParticipant.participant_type` + `participant_id`
- `message_type` maps to `Message.type`
- `entity_type` + `entity_id` maps to `Stream.scope` + `scope_id`
- When streams land, migrate extended comments → messages table, add `stream_id` foreign key, deprecate direct entity scoping

The comment extensions are a stepping stone, not a dead end.

## Consequences

### Positive
- GUI-based A2A messaging without building the full streams infrastructure
- Volon owns the messaging primitive — full control, no CLI dependency
- Type-safe routing with identity hashing — no stringly-typed author fields
- Inbox paradigm eliminates infinite polling loops
- Stuck detection prevents tasks from silently stalling
- Backward compatible — existing `volon_comment_add` callers unaffected
- Clear migration path to ADR-009 unified streams

### Negative
- Two A2A systems coexist temporarily (file-based inbox for CLI, Volon comments for GUI)
- Comment table gains complexity — 7 new columns
- Queue Controller and Context Agent are new Special Agent profiles to maintain
- Stuck detection thresholds need tuning per use case

### Risks
- Comment volume on busy tasks could grow large — may need pagination or archival strategy
- Identity hashing scheme must be consistent across Nanite sessions and agent instances
- Queue Controller is a single point of failure for automated routing — needs health monitoring

## Alternatives considered

1. **Build full ADR-009 streams first** — Rejected. Too much upfront work before we can demo anything. The comment extension gives us 80% of the value immediately.
2. **Port file-based inbox to Volon** — Rejected for this scope. CLI agents work fine with files. GUI needs a different primitive (database-backed, queryable, renderable).
3. **Use Nanite's own SQLite for messaging** — Rejected. Messages belong in Volon (the system of record for tasks). Nanite is the harness, not the data owner.
4. **Add a separate `messages` table now** — Considered. But extending comments is simpler, keeps one table, and the migration to streams is the same effort either way.

## References

- ADR-009: Unified Messaging Streams (end-state design)
- ADR-022: Special Agent Integration Architecture (agent layering)
- ADR-024: Agent Context Architecture (context continuity)
- `/Users/chrispian/Projects-apps/volon/docs/unified-messaging-architecture.md` (full data model)
- `/Users/chrispian/Projects-apps/volon/internal/persistence/migrations/0039_comments.sql` (current schema)
