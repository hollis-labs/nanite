CREATE TABLE IF NOT EXISTS broker_decisions (
    id INTEGER PRIMARY KEY,
    session_id TEXT,
    intent TEXT,
    layer_reached TEXT,
    selected_tools TEXT,
    signals TEXT,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_broker_decisions_session ON broker_decisions(session_id);
