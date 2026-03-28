-- Add developer_mode and recover_mode flags to user_settings.
ALTER TABLE user_settings ADD COLUMN developer_mode INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_settings ADD COLUMN recover_mode INTEGER NOT NULL DEFAULT 0;
