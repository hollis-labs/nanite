-- +goose Up
-- Plugin installation uses archive checksums. Deploy and restart a binary
-- without queries for these fields before applying this migration.
ALTER TABLE catalog_sources DROP COLUMN public_key;
ALTER TABLE user_settings DROP COLUMN allow_unsigned_plugins;

-- +goose Down
-- Restore the old schema only. Retired signing keys and bypass preferences
-- cannot be recovered by rollback and default to empty / disabled.
ALTER TABLE catalog_sources ADD COLUMN public_key TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN allow_unsigned_plugins INTEGER NOT NULL DEFAULT 0;
