-- +goose Up
-- A subagent that stops at a step, token or context limit finishes with
-- status 'limit' and the stop reason, not 'succeeded'. SQLite cannot alter a
-- CHECK constraint, so the table is rebuilt. Nothing references jobs.
CREATE TABLE jobs_next (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 kind TEXT NOT NULL CHECK(kind IN ('shell','subagent')),
 spec_json TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('running','succeeded','limit','failed','cancelled','lost')),
 pid INTEGER,
 output_path TEXT,
 exit_code INTEGER,
 started_at TEXT NOT NULL,
 ended_at TEXT,
 process_identity TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT ''
);
INSERT INTO jobs_next(id,conversation_id,kind,spec_json,status,pid,output_path,exit_code,started_at,ended_at,process_identity)
 SELECT id,conversation_id,kind,spec_json,status,pid,output_path,exit_code,started_at,ended_at,process_identity FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_next RENAME TO jobs;
CREATE INDEX jobs_by_conversation_and_kind ON jobs(conversation_id,kind,started_at,id);
