-- +goose Up
-- Phase 3 S2a (2026-04-15): user-configurable embedding provider.
--
-- Embedders are off by default (privacy-first). When embedding_mode = 'disabled'
-- or embedding_provider = '', no embedder is wired into the memory store and
-- similarity recall is unavailable. Setting embedding_mode = 'explicit' with a
-- supported provider activates embedding (subject to credential / reachability
-- checks done at container wiring time).
ALTER TABLE user_settings ADD COLUMN embedding_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN embedding_model TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN embedding_mode TEXT NOT NULL DEFAULT 'disabled';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
