-- Plugin catalog sources: remote registries from which plugins can be discovered and installed.
-- Multiple sources supported (Composer-style). Higher priority sources win on name conflicts.

CREATE TABLE IF NOT EXISTS catalog_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'custom',
    enabled INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 0,
    public_key TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_sources_url ON catalog_sources(url);

-- Seed the official catalog source.
INSERT OR IGNORE INTO catalog_sources (id, name, url, type, priority)
VALUES ('official', 'Hollis Labs', 'https://raw.githubusercontent.com/hollis-labs/conduit-plugins/main/catalog.yaml', 'official', 100);
