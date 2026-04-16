-- S7 T3: Messaging channels.
--
-- Introduces a transport-bucket column on the messaging table.
-- `chat`   — in-session conversational traffic (primary ↔ secondary)
-- `inbox`  — async polled, triggers notifications on arrival
-- `alert`  — agent-triggered one-off ("report ready"), toast + inbox
--
-- Channel usage is POLICY not code for MVP — the CHECK constraint
-- bounds the value set, but what to send on which channel is a
-- convention documented in docs/messaging.md, not enforced in the
-- application layer.

ALTER TABLE a2a_messages ADD COLUMN channel TEXT NOT NULL DEFAULT 'chat'
  CHECK(channel IN ('chat','inbox','alert'));

-- Inbox + subscribe queries filter by (to_session_id, to_agent_id,
-- status) with channel as an optional additional filter. Index order
-- mirrors the WHERE clause composition so both with-channel and
-- without-channel queries get index use.
CREATE INDEX IF NOT EXISTS idx_a2a_messages_channel
  ON a2a_messages(to_session_id, to_agent_id, channel, status, created_at DESC);
