-- Add tool stream behavior and drawer retention settings.
ALTER TABLE user_settings ADD COLUMN tool_stream_behavior TEXT NOT NULL DEFAULT 'streaming';
ALTER TABLE user_settings ADD COLUMN tool_drawer_retention INTEGER NOT NULL DEFAULT 15;
