-- +goose Up
-- Drop messaging_envelopes — dead table, TASKS/phase-0/18b.
--
-- Architecture doc docs/engineering/architecture/07-inter-agent-messaging.md
-- ("Cut"): internal/messaging/gomsg (this table's only backing package) was
-- "fully built, contract-tested, never constructed anywhere the process
-- boots." Verified directly: NewSQLStore/NewRouter (gomsg's sqlstore.go/
-- federation.go) were called only from the package's own test files —
-- zero production construction call sites anywhere in the codebase.
--
-- The one real production dependency on the gomsg package turned out to be
-- internal/messaging/envelope_bridge.go (ToEnvelope/FromEnvelope), which
-- is itself dead: those two functions had zero callers outside their own
-- definitions and tests. Both internal/messaging/gomsg/ and
-- envelope_bridge.go(+test) were deleted alongside this migration — see
-- TASKS/phase-0/18b-cut-dead-messaging-and-plugin-tables.md's Work Log.
--
-- The Down recreates the table exactly as migration 065 defined it, so a
-- rollback restores schema shape (not data — the table's contents, if any
-- ever existed, are not recoverable from a drop).

DROP TABLE IF EXISTS messaging_envelopes;

-- +goose Down

CREATE TABLE IF NOT EXISTS messaging_envelopes (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    channel      TEXT NOT NULL DEFAULT '',
    from_urn     TEXT NOT NULL,
    to_urn       TEXT NOT NULL,
    thread_id    TEXT NOT NULL DEFAULT '',
    in_reply_to  TEXT NOT NULL DEFAULT '',
    payload      TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    metadata     TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL,
    delivered_at TEXT,
    consumed_at  TEXT,
    canceled     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_messaging_envelopes_inbox
    ON messaging_envelopes(to_urn, delivered_at, canceled, created_at);

CREATE INDEX IF NOT EXISTS idx_messaging_envelopes_thread
    ON messaging_envelopes(thread_id, created_at);
