-- FU-34 per-tick mailbox digest dedup table.
--
-- The composer mail source (internal/composer/source_mail.go) reads
-- unread messages from the mux store at ~/tether/state/tether.db and
-- renders a markdown digest for the agent per-tick boot procedure. To
-- keep the digest idempotent across re-composes within the same tick
-- (and to avoid replaying the same message in subsequent ticks before
-- the agent acknowledges via mux_message_mark_read), we record which
-- message ids have already been surfaced to a given agent URN.
--
-- viewed_at is informational for operator triage. The PK is
-- (message_id, agent_urn). tick_n is the agridd-side tick counter the
-- digest landed on, useful for traceability when re-rendering history.
--
-- IF NOT EXISTS makes this idempotent across the every-boot migration
-- replay. The runner splits on the semicolon character even inside
-- comments, so this file keeps comment lines semicolon-free.

CREATE TABLE IF NOT EXISTS agent_mailbox_view (
    message_id TEXT NOT NULL,
    agent_urn  TEXT NOT NULL,
    tick_n     INTEGER NOT NULL,
    viewed_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (message_id, agent_urn)
);

CREATE INDEX IF NOT EXISTS idx_agent_mailbox_view_agent ON agent_mailbox_view(agent_urn, tick_n)
