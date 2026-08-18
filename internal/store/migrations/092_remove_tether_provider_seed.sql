-- +goose Up
-- +goose NO TRANSACTION
-- 091_remove_tether_provider_seed.sql
-- CW-20260812-0002
--
-- The Tether integration was deleted from Nanite core (PR #254):
-- internal/llm/tether, internal/muxproxy, and the "tether" provider
-- registration in initProviders are all gone, so nothing in the running
-- binary can ever route a chat request to the tether-001 provider anymore.
--
-- internal/store/seed.go's SeedProviders uses INSERT OR IGNORE, so removing
-- the seed entries from the Go slice only stops *new* databases from ever
-- getting these rows — it does nothing for a database that was already
-- seeded before the deletion. Without this migration, tether-001 / tether-auto
-- stay visible and selectable in the provider/model UI on every existing
-- install, and selecting them now fails at runtime with no registered
-- adapter behind them (a working option turned into a silently broken one).
--
-- Idempotent: DELETE on non-existent rows is a no-op. models.provider_id
-- references providers.id, so the child row is removed first.

BEGIN;

DELETE FROM models WHERE id = 'tether-auto' OR provider_id = 'tether-001';
DELETE FROM providers WHERE id = 'tether-001' OR provider_type = 'tether';

COMMIT;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
