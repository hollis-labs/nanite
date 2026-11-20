-- +goose Up
-- Settings a person made for one conversation, such as its deliberate mode.
-- They outlive a restart and go with the conversation when it is deleted.
CREATE TABLE conversation_settings (
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (conversation_id, key)
);

