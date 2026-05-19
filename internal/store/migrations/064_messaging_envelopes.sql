-- Envelope-native messaging tables — the shared go-messaging contract.
--
-- CW-20260518-0050: Nanite adopts the portfolio-shared go-messaging
-- contract (Envelope, typed URN Address, closed Kind enum) so it is a
-- first-class peer for app-to-app agent/user messaging.
--
-- The legacy agent_messages table (tuple-addressed, from/to session and
-- agent pairs) is intentionally left in place. This is an additive,
-- extend-don't-rebuild migration. messaging_envelopes is the durable
-- backing store for internal/messaging/gomsg.SQLStore, which satisfies
-- messaging.Store identically to the in-memory reference impl, verified
-- by go-messaging's messagingtest.RunContract suite.
--
-- Addressing is by canonical URN msg://<kind>/<authority>/<id>/<subid>
-- stored as opaque TEXT. The authority segment is the federation seam.
-- A standalone install uses one local authority. A federated peer
-- routes foreign authorities to peer stores. See internal/messaging/gomsg
-- and docs/messaging-federation.md.
--
-- The migration runner has no schema_migrations table, so every
-- migration re-runs on every boot. All DDL here is idempotent via
-- IF NOT EXISTS. NOTE: the runner splits statements on the semicolon
-- character, including inside comments, so this file uses none.

CREATE TABLE IF NOT EXISTS messaging_envelopes (
    -- Store-assigned UUIDv7, monotonic, doubles as the chronological
    -- tie-break key alongside created_at.
    id           TEXT PRIMARY KEY,
    -- Closed go-messaging Kind enum: request, response, notice,
    -- status_update, handoff, escalation.
    kind         TEXT NOT NULL,
    -- Opaque UX-layer channel pass-through, empty when unset.
    channel      TEXT NOT NULL DEFAULT '',
    -- Canonical URN endpoints.
    from_urn     TEXT NOT NULL,
    to_urn       TEXT NOT NULL,
    thread_id    TEXT NOT NULL DEFAULT '',
    in_reply_to  TEXT NOT NULL DEFAULT '',
    -- Inline JSON payload plus its content type.
    payload      TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    -- map[string]string serialized as a JSON object, '{}' when empty.
    metadata     TEXT NOT NULL DEFAULT '{}',
    -- RFC3339 with forced 9-digit fractional seconds so lexical order
    -- equals chronological order, fixed-width, see gomsg.timeFormat.
    created_at   TEXT NOT NULL,
    -- Per-recipient lifecycle. NULL until Inbox delivers or Consume acks.
    delivered_at TEXT,
    consumed_at  TEXT,
    -- Set by Cancel. A dead envelope is excluded from Inbox and Subscribe.
    canceled     INTEGER NOT NULL DEFAULT 0
);

-- Inbox path: undelivered envelopes for a recipient, chronological.
CREATE INDEX IF NOT EXISTS idx_messaging_envelopes_inbox
    ON messaging_envelopes(to_urn, delivered_at, canceled, created_at);

-- Thread path: all envelopes in a conversation, chronological.
CREATE INDEX IF NOT EXISTS idx_messaging_envelopes_thread
    ON messaging_envelopes(thread_id, created_at);
