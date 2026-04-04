-- Add task_backend setting column to user_settings.
-- Values: "local" (default), or a plugin-provided backend name.
ALTER TABLE user_settings ADD COLUMN task_backend TEXT NOT NULL DEFAULT 'local';
