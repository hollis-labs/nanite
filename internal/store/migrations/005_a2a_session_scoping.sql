-- +goose Up
-- A2A session scoping: clean break on addressing columns.

-- Drop the old inbox index that references to_agent before dropping the column.
DROP INDEX IF EXISTS idx_a2a_inbox;

-- Drop unconstrained columns (clean break, no current users).
ALTER TABLE a2a_messages DROP COLUMN from_agent;
ALTER TABLE a2a_messages DROP COLUMN to_agent;

-- Add session-scoped addressing.
ALTER TABLE a2a_messages ADD COLUMN from_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN from_agent_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN to_session_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN to_agent_id     TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_a2a_to_session_agent   ON a2a_messages(to_session_id, to_agent_id, status);
CREATE INDEX IF NOT EXISTS idx_a2a_from_session_agent ON a2a_messages(from_session_id, from_agent_id);

-- Handoff audit trail. The session_agents junction table handles the binding
-- itself (via is_primary) -- this table only tracks the request and approval lifecycle.
CREATE TABLE IF NOT EXISTS session_handoffs (
    id                    TEXT PRIMARY KEY,
    session_id            TEXT NOT NULL REFERENCES sessions(id),
    from_agent_id         TEXT,
    to_agent_id           TEXT NOT NULL,
    requested_by          TEXT NOT NULL,    -- "departing" | "incoming" | "user"
    status                TEXT NOT NULL DEFAULT 'pending'
                              CHECK(status IN ('pending','approved','rejected','completed')),
    requested_at          TEXT NOT NULL,
    approved_at           TEXT,
    approved_by_user      INTEGER NOT NULL DEFAULT 0,
    context_message_count INTEGER,
    notes                 TEXT
);

CREATE INDEX IF NOT EXISTS idx_handoffs_session ON session_handoffs(session_id, status);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
