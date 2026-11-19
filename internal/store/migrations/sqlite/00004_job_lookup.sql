-- +goose Up
CREATE INDEX jobs_by_conversation_and_kind ON jobs(conversation_id,kind,started_at,id);
