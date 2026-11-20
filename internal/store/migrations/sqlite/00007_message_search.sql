-- +goose Up
-- Full-text index over each message's text, so a subtask can search its
-- parent's history (parent_history_search). The index is only matched and
-- ranked; results are read back from messages and redacted by the caller.
-- Triggers keep it in step with messages, including the index rebuild, which
-- deletes and replays them. Thinking blocks are not indexed.
CREATE VIRTUAL TABLE messages_fts USING fts5(body, conversation_id UNINDEXED, tokenize = 'unicode61');

-- +goose StatementBegin
CREATE TRIGGER messages_fts_insert AFTER INSERT ON messages BEGIN
 INSERT INTO messages_fts(rowid, body, conversation_id) VALUES (new.rowid,
  (SELECT group_concat(trim(coalesce(json_extract(value, '$.text'), '') || ' ' || coalesce(json_extract(value, '$.content'), '') || ' ' ||
    coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.input'), '')), ' ')
   FROM json_each(new.blocks_json) WHERE coalesce(json_extract(value, '$.type'), '') != 'thinking'),
  new.conversation_id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages BEGIN
 DELETE FROM messages_fts WHERE rowid = old.rowid;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER messages_fts_update AFTER UPDATE OF blocks_json, conversation_id ON messages BEGIN
 DELETE FROM messages_fts WHERE rowid = old.rowid;
 INSERT INTO messages_fts(rowid, body, conversation_id) VALUES (new.rowid,
  (SELECT group_concat(trim(coalesce(json_extract(value, '$.text'), '') || ' ' || coalesce(json_extract(value, '$.content'), '') || ' ' ||
    coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.input'), '')), ' ')
   FROM json_each(new.blocks_json) WHERE coalesce(json_extract(value, '$.type'), '') != 'thinking'),
  new.conversation_id);
END;
-- +goose StatementEnd

INSERT INTO messages_fts(rowid, body, conversation_id)
 SELECT m.rowid,
  (SELECT group_concat(trim(coalesce(json_extract(value, '$.text'), '') || ' ' || coalesce(json_extract(value, '$.content'), '') || ' ' ||
    coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.input'), '')), ' ')
   FROM json_each(m.blocks_json) WHERE coalesce(json_extract(value, '$.type'), '') != 'thinking'),
  m.conversation_id
 FROM messages m;
