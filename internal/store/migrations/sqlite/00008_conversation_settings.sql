-- +goose Up
-- Settings a person made for one conversation, such as its deliberate mode.
-- They outlive a restart. There is no foreign key to conversations: those
-- rows are a projection of the transcripts that RebuildIndex deletes and
-- replays, and the settings are not in the transcripts.
CREATE TABLE conversation_settings (
  conversation_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (conversation_id, key)
);
