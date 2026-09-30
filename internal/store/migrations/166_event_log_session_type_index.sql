-- +goose Up
-- 166_event_log_session_type_index.sql
--
-- The write-claim guard reads a session's newest write-result rows
-- (WHERE session_id = ? AND event_type = ? ORDER BY id DESC LIMIT n). With only
-- idx_event_log_session (session_id, created_at) SQLite filtered every event of
-- the session and sorted the matches in a temporary b-tree before applying the
-- limit. This index serves that read in order, straight to the newest rows.
--
-- Index only: no column or row changes, so a running process is unaffected.

CREATE INDEX IF NOT EXISTS idx_event_log_session_type ON event_log(session_id, event_type, id);

-- +goose Down
DROP INDEX IF EXISTS idx_event_log_session_type;
