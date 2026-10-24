-- +goose Up
-- Install identity: one random id per Nanite database, the install component of
-- the Tether external key nanite/<install_id>/<host_settings_id>. The single row
-- is minted by the application on first boot after this migration (Go, not SQL:
-- see Store.EnsureInstallID); this migration creates the table empty.
-- previous_install_id and regenerated_at record the last regeneration only; the
-- full history is in event_log (event_type 'install_identity_regenerated').
CREATE TABLE IF NOT EXISTS install_identity (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    install_id          TEXT NOT NULL CHECK (length(trim(install_id)) > 0),
    previous_install_id TEXT NOT NULL DEFAULT '',
    regenerated_at      TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- +goose Down
DROP TABLE IF EXISTS install_identity;
