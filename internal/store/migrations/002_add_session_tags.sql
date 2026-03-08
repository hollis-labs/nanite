-- Add tags column to sessions table (JSON array of strings).
ALTER TABLE sessions ADD COLUMN tags TEXT DEFAULT '[]';
