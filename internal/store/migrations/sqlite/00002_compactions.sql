-- +goose Up
CREATE TABLE compactions (
 seq INTEGER PRIMARY KEY,
 id TEXT NOT NULL UNIQUE,
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 turn_id TEXT NOT NULL REFERENCES turns(id),
 through_message_id TEXT NOT NULL REFERENCES messages(id),
 summary TEXT NOT NULL
);
CREATE INDEX compactions_by_conversation ON compactions(conversation_id,seq);
