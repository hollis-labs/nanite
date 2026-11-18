-- +goose Up
CREATE TABLE conversations (
 id TEXT PRIMARY KEY,
 parent_id TEXT,
 role TEXT NOT NULL,
 title TEXT NOT NULL,
 status TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE turns (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 seq INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('running','completed','failed','cancelled','interrupted')),
 user_message_id TEXT NOT NULL,
 started_at TEXT NOT NULL,
 ended_at TEXT,
 stop_reason TEXT NOT NULL DEFAULT '',
 usage_json TEXT NOT NULL DEFAULT '{}',
 error TEXT NOT NULL DEFAULT '',
 UNIQUE(conversation_id,seq)
);
CREATE TABLE messages (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 turn_id TEXT NOT NULL REFERENCES turns(id),
 seq INTEGER NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('user','assistant','tool')),
 blocks_json TEXT NOT NULL,
 transcript_cursor TEXT NOT NULL,
 created_at TEXT NOT NULL,
 UNIQUE(conversation_id,seq)
);
CREATE TABLE transcript_cursors (
 conversation_id TEXT PRIMARY KEY REFERENCES conversations(id),
 cursor TEXT NOT NULL
);
CREATE TABLE todos (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 content TEXT NOT NULL,
 status TEXT NOT NULL,
 position INTEGER NOT NULL
);
CREATE TABLE scratchpad (
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 key TEXT NOT NULL,
 content TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(conversation_id,key)
);
CREATE TABLE jobs (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 kind TEXT NOT NULL CHECK(kind IN ('shell','subagent')),
 spec_json TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','cancelled','lost')),
 pid INTEGER,
 output_path TEXT,
 exit_code INTEGER,
 started_at TEXT NOT NULL,
 ended_at TEXT
);
CREATE TABLE notices (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 kind TEXT NOT NULL,
 body TEXT NOT NULL,
 created_at TEXT NOT NULL,
 delivered_turn_id TEXT REFERENCES turns(id)
);
CREATE TABLE tool_results (
 scope TEXT NOT NULL,
 id TEXT NOT NULL,
 tool_name TEXT NOT NULL,
 body TEXT NOT NULL,
 byte_size INTEGER NOT NULL,
 expires_at TEXT,
 PRIMARY KEY(scope,id)
);
