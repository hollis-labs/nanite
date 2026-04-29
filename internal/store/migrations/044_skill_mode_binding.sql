-- E2 (CW-20260428-0017): mode binding for skills.
-- Adds a JSON array column "mode_ids" to skills storing the mode IDs the
-- skill is bound to. Empty / "[]" / NULL means available in every mode
-- (back-compat default).
-- Slugs in the file frontmatter are translated to mode IDs at ingest time.
-- Migration runner swallows duplicate-column errors for ADD COLUMN.
-- IMPORTANT: no semicolons inside comments.

ALTER TABLE skills
    ADD COLUMN mode_ids TEXT NOT NULL DEFAULT '[]';
