# Messaging — upgrade path to stream-scoped model

**Status:** Design note (T11). No code changes here; this documents the shape so future migration is column-add, not rewrite.

## Context

Nanite's messaging subsystem today addresses messages with two tuples:

```
(from_session_id, from_agent_id)  →  (to_session_id, to_agent_id)
```

Each row in `agent_messages` carries both tuples plus a `thread_id` that groups messages into a conversation.

Engine's `docs/unified-messaging-architecture.md` points at a different shape for the long term: a **stream-scoped** model where a `stream_id` plus a `participants[]` array replaces the denormalized from/to tuples. A stream is like a group chat: any number of agents can participate, each addressed by their id within the stream.

The portfolio-level goal is for Nanite and Engine to converge on the stream-scoped model so messaging tooling (indexing, search, replay) is shared. S7 does NOT attempt that migration — it's plenty of surface on its own. S7 DOES make the eventual migration column-add-only by disciplining the current shape.

## Shape-compatibility rules honored in S7

1. **`thread_id` is the seed for future `stream_id`.** The column is already `TEXT`; no fixed-length assumption in the Go-side `Message.ThreadID`. When the stream-scoped migration lands it renames the column (or adds `stream_id` and back-fills from `thread_id`), without a data-type coercion.

2. **`from_*` and `to_*` columns stay denormalized but TEXT-sized.** They carry single ids today; the migration will add a `participants_json` column and keep these as read-only denormalized copies until a later cleanup drops them. No fixed-length check constraint.

3. **Store interface method names match Nexus where possible.** `Send`, `Get`, `Inbox`, `Thread`, `Ack`, `Resolve`, `UnreadCount` are all there. When `hollis-labs/go-messaging` extraction happens the move-and-rename is a file operation, not an interface redesign.

4. **Go-side `Store` interface does not bake single-sender / single-receiver assumptions.** The method surface takes tuple-pair strings today, but Adding a `SendMulti(ctx, MultiMessage)` method when stream-scoped messaging needs multi-party send is additive, not breaking (Go interfaces are satisfied structurally).

## Migration plan (NOT executed in S7)

When the stream-scoped migration lands, roughly:

```sql
-- Add the stream-scoped columns.
ALTER TABLE agent_messages ADD COLUMN stream_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_messages ADD COLUMN participants_json TEXT NOT NULL DEFAULT '[]';

-- Back-fill from existing tuples. Every existing row's thread_id becomes
-- its stream_id; participants are the two ends of the tuple.
UPDATE agent_messages
   SET stream_id = COALESCE(thread_id, id),
       participants_json = json_array(
         json_object('session_id', from_session_id, 'agent_id', from_agent_id),
         json_object('session_id', to_session_id,   'agent_id', to_agent_id)
       )
 WHERE stream_id = '';

-- New index for stream lookups.
CREATE INDEX idx_agent_messages_stream ON agent_messages(stream_id, created_at);
```

Later (separate cleanup migration once no reader depends on the tuples):

```sql
ALTER TABLE agent_messages DROP COLUMN from_session_id;
ALTER TABLE agent_messages DROP COLUMN from_agent_id;
ALTER TABLE agent_messages DROP COLUMN to_session_id;
ALTER TABLE agent_messages DROP COLUMN to_agent_id;
-- thread_id stays as the human-readable alias for stream_id, or gets
-- dropped too if participants_json makes it redundant. Decide at
-- migration time based on query patterns.
```

## On the Go-side Store interface

The current `Store.Send(ctx, input SendInput) (*Message, error)` is fine for single-addressee sends (the 99% case today). The migration adds:

```go
type MultiMessage struct {
    StreamID     string
    From         Participant  // single sender
    Participants []Participant  // read-eligible set
    Body         string
    // ... kind/channel/payload as today
}

type Participant struct {
    SessionID string
    AgentID   string
}

// New method; doesn't break existing Send callers.
SendMulti(ctx context.Context, m MultiMessage) (*Message, error)
```

`Send(input SendInput)` can stay as a thin wrapper over `SendMulti` during the transition, or be retired once all callers move over.

## When this migration happens

Not in S7. Trigger is either:
- Engine team ships their stream-scoped implementation and the nanite side needs to consume its events.
- A second nanite feature genuinely needs multi-party addressing (group-chat rooms, broadcast to all session agents, etc.).
- A portfolio BLG schedules it explicitly.

Captured as a `phase-4-followup` / `portfolio-alignment` BLG on Engine.
