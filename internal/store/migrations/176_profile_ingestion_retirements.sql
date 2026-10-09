-- +goose Up
-- Deliberate protected-class retirement survives deletion, boot and plugin reload.
-- This audit has no FK to the deleted profile or to the retained logical profile.
CREATE TABLE profile_ingestion_retirements (
    profile_id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    source TEXT NOT NULL,
    plugin_id TEXT NOT NULL,
    export_id TEXT NOT NULL UNIQUE,
    digest TEXT NOT NULL,
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    keep_profile_id TEXT NOT NULL,
    keep_revision TEXT NOT NULL,
    retired_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- +goose Down
DROP TABLE profile_ingestion_retirements;
