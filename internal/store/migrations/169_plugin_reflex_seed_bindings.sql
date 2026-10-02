-- +goose Up
-- Durable ownership survives unloading and definition deletion. NULL reflex_id
-- is a tombstone: reload must not silently undo an operator's deletion.
CREATE TABLE plugin_reflex_seed_bindings (
    plugin_id TEXT NOT NULL,
    seed_id TEXT NOT NULL,
    agent_id TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    reflex_id TEXT UNIQUE REFERENCES agent_reflexes(id) ON DELETE SET NULL,
    PRIMARY KEY (plugin_id, seed_id, agent_id)
);

-- +goose Down
DROP TABLE plugin_reflex_seed_bindings;
