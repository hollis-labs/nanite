-- +goose Up
ALTER TABLE turns ADD COLUMN agent_dir TEXT NOT NULL DEFAULT '';
ALTER TABLE turns ADD COLUMN headers_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE turns DROP COLUMN headers_json;
ALTER TABLE turns DROP COLUMN agent_dir;
