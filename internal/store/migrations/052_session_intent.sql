-- Glass-3 (CW-20260502-0011, SP-20260502-0001) — session intent for auto-handoff classification.
-- NULL means unclassified. Classification fires in Glass-4 at session start.
-- Enum values long-running, per-turn, ephemeral are enforced by the CHECK below.
-- NOTE no semicolons inside comments (splitSQL naive-split limitation).

ALTER TABLE sessions ADD COLUMN intent TEXT NULL CHECK (intent IS NULL OR intent IN ('long-running', 'per-turn', 'ephemeral'));
