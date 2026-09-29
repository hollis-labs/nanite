-- +goose Up
-- 163_tool_call_arguments.sql
--
-- Durable, bounded, redacted record of the arguments a tool was called with
-- (CW-20260919-0006 / D-35). For audit and observability only: nothing here is
-- ever placed in the model's context. body holds the redacted arguments run
-- through the same preview budget as tool results (internal/tool/preview.go),
-- never the raw original; sha256 is of the redacted full document, so equal
-- hashes mean equal redacted arguments without exposing low-entropy secrets to
-- a guessing attack.

CREATE TABLE IF NOT EXISTS tool_call_arguments (
    id TEXT PRIMARY KEY,                    -- ULID
    session_id TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,             -- LLM's tool_use_id
    tool_name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    byte_size INTEGER NOT NULL,             -- redacted document size before the budget
    sha256 TEXT NOT NULL,                   -- of the redacted full document
    was_truncated INTEGER NOT NULL DEFAULT 0,
    redacted_count INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tool_call_arguments_session ON tool_call_arguments(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_tool_call_arguments_expires ON tool_call_arguments(expires_at);

-- +goose Down
DROP TABLE IF EXISTS tool_call_arguments;
