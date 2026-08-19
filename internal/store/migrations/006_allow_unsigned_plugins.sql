-- +goose Up
-- J.2 (Phase 2 Track J.2, 2026-04-14): add a dev-only opt-in that permits
-- installing plugin archives without a verified Ed25519 signature.
--
-- The value is INERT in production builds. The signature-verify call site
-- gates this field behind the `devmode` build tag (internal/plugin/devmode):
-- production binaries compile the gate to a const false so a compromised
-- user_settings row cannot weaken signing policy. Only dev builds
-- (`make build-dev`) honour the field.
ALTER TABLE user_settings ADD COLUMN allow_unsigned_plugins INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
