-- +goose Up
-- Drop agent_mailbox_view — dead table, TASKS/phase-0/18b.
--
-- Despite the name, this is a TABLE (see migration 077), not a SQL VIEW.
--
-- Architecture doc docs/engineering/architecture/07-inter-agent-messaging.md
-- ("Cut"): "agent_mailbox_view — zero call sites anywhere, references a
-- file (internal/composer/source_mail.go) that doesn't exist in the tree."
-- Verified directly: internal/composer/ does not exist anywhere in this
-- repo (the whole directory, not just that one file), and a repo-wide
-- case-insensitive grep for agent_mailbox_view found zero Go references
-- anywhere outside migration 077 itself — no store-layer code creates or
-- reads this table, so there was nothing else to remove alongside the
-- migration. See TASKS/phase-0/18b-cut-dead-messaging-and-plugin-tables.md's
-- Work Log.
--
-- NOTE: comment lines in this file deliberately avoid semicolons — goose's
-- line-based SQL parser splits statements on any semicolon it sees,
-- including inside a "--" comment (matches the convention documented in
-- migration 077's own header).
--
-- The Down recreates the table exactly as migration 077 defined it.

DROP TABLE IF EXISTS agent_mailbox_view;

-- +goose Down

CREATE TABLE IF NOT EXISTS agent_mailbox_view (
    message_id TEXT NOT NULL,
    agent_urn  TEXT NOT NULL,
    tick_n     INTEGER NOT NULL,
    viewed_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (message_id, agent_urn)
);

CREATE INDEX IF NOT EXISTS idx_agent_mailbox_view_agent ON agent_mailbox_view(agent_urn, tick_n);
