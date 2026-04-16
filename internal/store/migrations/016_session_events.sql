-- S7 T8: Session event-log.
--
-- First-class replayable session events. Messages join tool-calls
-- and user-responses as events that can be queried by session in
-- chronological order for context assembly and replay.
--
-- For T8 the recorded event types are:
--   message_sent       — recorded on the sender's session
--   message_received   — recorded on the recipient's session
--   message_acked      — recorded on the recipient's session on ack
--   message_resolved   — recorded on the recipient's session on resolve
--
-- envelope_pointer_json carries enough context to rehydrate the
-- envelope from elsewhere (message_id + from/to address tuples).
-- Kept as TEXT so later event types can carry different payload
-- shapes without a schema migration.

CREATE TABLE IF NOT EXISTS session_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    channel TEXT NOT NULL DEFAULT '',
    envelope_pointer_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_session_events_session_created
  ON session_events(session_id, created_at);

CREATE INDEX IF NOT EXISTS idx_session_events_type
  ON session_events(event_type, created_at);
