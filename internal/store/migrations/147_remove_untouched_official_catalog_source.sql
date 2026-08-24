-- +goose Up
-- TASKS/audit-remediation/14-followups/
-- 01-remove-default-seeded-catalog-source.md.
--
-- AD-05 removed Nanite's built-in "official" catalog source because it has
-- no trusted public key and therefore cannot satisfy the fail-closed install
-- pipeline. Removing the Go seed only fixes fresh databases; this migration
-- removes the row from an existing database only when it is still the exact,
-- untouched legacy seed. Every seeded field is part of the predicate, and
-- created_at = updated_at proves no write through the supported update/key
-- paths has touched the row. Keyed, customized, disabled, reprioritized, and
-- unrelated operator-owned sources are preserved.
DELETE FROM catalog_sources
WHERE id = 'official'
  AND name = 'Hollis Labs'
  AND url = 'https://raw.githubusercontent.com/hollis-labs/plugin-catalog/main/catalog.yaml'
  AND type = 'official'
  AND enabled = 1
  AND priority = 100
  AND public_key = ''
  AND created_at = updated_at;

-- +goose Down
-- Recreate the exact legacy seed shape without replacing an operator-owned
-- row that already occupies either the id or unique URL.
INSERT OR IGNORE INTO catalog_sources (id, name, url, type, priority)
VALUES (
    'official',
    'Hollis Labs',
    'https://raw.githubusercontent.com/hollis-labs/plugin-catalog/main/catalog.yaml',
    'official',
    100
);
