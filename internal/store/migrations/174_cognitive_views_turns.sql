-- +goose Up
-- Native API content/pins are separate from mutable product metadata and
-- retained profile administration. These rows create no fabric identity.
CREATE TABLE cognitive_views (
    session_view_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    definition_ref_json TEXT NOT NULL,
    chat_config_json TEXT NOT NULL
);
CREATE TABLE cognitive_turns (
    turn_id TEXT PRIMARY KEY,
    session_view_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    snapshot_json TEXT NOT NULL
);
CREATE INDEX cognitive_turns_view ON cognitive_turns(session_view_id);

-- +goose Down
DROP TABLE cognitive_turns;
DROP TABLE cognitive_views;
