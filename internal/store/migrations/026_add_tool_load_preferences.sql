-- User-level tool load preferences (loadType overrides).
-- Stored as JSON: {"tool_name": "auto|opt-in", ...}
ALTER TABLE user_settings ADD COLUMN tool_load_preferences TEXT NOT NULL DEFAULT '{}';
