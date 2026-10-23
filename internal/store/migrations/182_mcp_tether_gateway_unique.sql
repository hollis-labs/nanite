-- +goose Up
-- One Tether messaging gateway per host (CW-20261023-0022). The gateway entry
-- is host-wide and authenticates as one actor, so a second one under another
-- name would present a second credential to the same agents. The rule is a
-- unique partial index so create/create, update/update and create/update are
-- decided by the database, not by a read-then-write in application code.
-- tether_gateway is set by the store when a row runs `tether ... --token-file`.
ALTER TABLE mcp_servers ADD COLUMN tether_gateway INTEGER NOT NULL DEFAULT 0;

-- Rows that predate the rule: mark the oldest gateway only, so the index can
-- always be created. A later duplicate stays unmarked and is left alone.
UPDATE mcp_servers SET tether_gateway = 1
 WHERE rowid = (SELECT rowid FROM mcp_servers
                 WHERE (command = 'tether' OR command LIKE '%/tether') AND args LIKE '%--token-file%'
                 ORDER BY created_at, rowid LIMIT 1);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_servers_one_tether_gateway
    ON mcp_servers(tether_gateway) WHERE tether_gateway = 1;

-- +goose Down
DROP INDEX IF EXISTS idx_mcp_servers_one_tether_gateway;
ALTER TABLE mcp_servers DROP COLUMN tether_gateway;
