-- +goose Up
-- Persisted Tether watch cursor per actor URN (MVP push, CW-20261024-0020).
-- after_seq is the highest Tether message seq the bridge has recorded in the
-- tether_deliveries ledger for that actor; the watch resumes after it, so a
-- restart replays only what was missed. Additive; holds no credential.
CREATE TABLE IF NOT EXISTS tether_watch_cursors (
    actor_urn  TEXT PRIMARY KEY,
    after_seq  INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- +goose Down
DROP TABLE IF EXISTS tether_watch_cursors;
