-- +goose Up
-- Private opaque version for the read-only admin preferences projection.
-- Additive: older binaries name their columns explicitly and ignore this one.
-- Existing rows are initialized by store.New before any caller can serve them.
ALTER TABLE user_settings ADD COLUMN admin_preferences_version TEXT NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE TRIGGER user_settings_admin_preferences_insert
AFTER INSERT ON user_settings
BEGIN
    UPDATE user_settings
       SET admin_preferences_version = lower(hex(randomblob(16)))
     WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- UPDATE OF excludes the version-only writes above/below, even with
-- recursive_triggers enabled. IS NOT compares actual values, including NULL.
-- +goose StatementBegin
CREATE TRIGGER user_settings_admin_preferences_update
AFTER UPDATE OF tool_stream_behavior, tool_drawer_retention ON user_settings
WHEN OLD.tool_stream_behavior IS NOT NEW.tool_stream_behavior
  OR OLD.tool_drawer_retention IS NOT NEW.tool_drawer_retention
BEGIN
    UPDATE user_settings
       SET admin_preferences_version = lower(hex(randomblob(16)))
     WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
-- Reverse only this migration; remove dependent triggers before the column.
DROP TRIGGER user_settings_admin_preferences_update;
DROP TRIGGER user_settings_admin_preferences_insert;
ALTER TABLE user_settings DROP COLUMN admin_preferences_version;
