-- J.2 (Phase 2 Track J.2, 2026-04-14): add a dev-only opt-in that permits
-- installing plugin archives without a verified Ed25519 signature.
--
-- The value is INERT in production builds. The signature-verify call site
-- gates this field behind the `devmode` build tag (internal/plugin/devmode):
-- production binaries compile the gate to a const false so a compromised
-- user_settings row cannot weaken signing policy. Only dev builds
-- (`make build-dev`) honour the field.
ALTER TABLE user_settings ADD COLUMN allow_unsigned_plugins INTEGER NOT NULL DEFAULT 0;
