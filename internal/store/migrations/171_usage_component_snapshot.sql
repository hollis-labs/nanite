-- +goose Up
-- Historical estimates stay unchanged; absent accounting remains explicitly partial.
ALTER TABLE token_usage ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE token_usage ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE token_usage ADD COLUMN cost_status TEXT NOT NULL DEFAULT 'PARTIAL';
ALTER TABLE token_usage ADD COLUMN cost_snapshot TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Structure-only: no pre-migration equivalent existed to restore, and a genuine
-- downgrade is expected to have no rows depending on data this Down discards.
-- Stop the newer binary before downgrading; this also permits migration replay.
ALTER TABLE token_usage DROP COLUMN cost_snapshot;
ALTER TABLE token_usage DROP COLUMN cost_status;
ALTER TABLE token_usage DROP COLUMN reasoning_tokens;
ALTER TABLE token_usage DROP COLUMN provider;
