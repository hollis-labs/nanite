-- +goose Up
CREATE INDEX notices_pending_by_conversation
ON notices(conversation_id,created_at,id) WHERE delivered_turn_id IS NULL;
