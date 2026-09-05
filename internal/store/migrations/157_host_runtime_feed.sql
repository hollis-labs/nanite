-- +goose Up
-- The host runtime feed is deliberately separate from session_events. The
-- latter is an agent-to-agent mailbox journal; this table is a bounded,
-- public projection of wrapper runtime activity with its own SSE cursor.
CREATE TABLE host_runtime_feed_heads (
    session_id              TEXT PRIMARY KEY,
    last_cursor             INTEGER NOT NULL DEFAULT 0 CHECK (last_cursor >= 0),
    last_runtime_generation INTEGER NOT NULL DEFAULT 0 CHECK (last_runtime_generation >= 0),
    pruned_through_cursor   INTEGER NOT NULL DEFAULT 0 CHECK (pruned_through_cursor >= 0),
    retention_dropped       INTEGER NOT NULL DEFAULT 0 CHECK (retention_dropped >= 0),
    updated_at              TEXT NOT NULL
);

CREATE TABLE host_runtime_feed_events (
    session_id       TEXT NOT NULL,
    cursor           INTEGER NOT NULL CHECK (cursor > 0),
    runtime_run_id   TEXT NOT NULL,
    runtime_generation INTEGER NOT NULL CHECK (runtime_generation > 0),
    source_event_id  TEXT NOT NULL,
    source_sequence  INTEGER NOT NULL CHECK (source_sequence >= 0),
    kind             TEXT NOT NULL,
    event_json       TEXT NOT NULL,
    occurred_at      TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    PRIMARY KEY (session_id, cursor),
    UNIQUE (session_id, runtime_run_id, source_event_id)
);

CREATE INDEX idx_host_runtime_feed_events_replay
    ON host_runtime_feed_events(session_id, cursor);

-- +goose Down
DROP TABLE IF EXISTS host_runtime_feed_events;
DROP TABLE IF EXISTS host_runtime_feed_heads;
