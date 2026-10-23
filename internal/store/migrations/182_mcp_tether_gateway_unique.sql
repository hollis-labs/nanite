-- +goose Up
-- One enabled Tether messaging gateway per host (CW-20261023-0022). The
-- gateway entry is host-wide and authenticates as one actor, so a second
-- enabled one under another name would present a second credential to the same
-- agents. The rule is a unique partial index so create/create, update/update
-- and create/update are decided by the database, not by a read-then-write in
-- application code. tether_gateway is set by the store on a row that is
-- enabled and runs `tether ... --token-file`.
ALTER TABLE mcp_servers ADD COLUMN tether_gateway INTEGER NOT NULL DEFAULT 0;

-- Fail closed on an install that already holds more than one enabled gateway
-- (possible before the rule existed). Nothing is disabled, deleted or exempted
-- on the operator's behalf: the migration refuses, and the transaction rolls
-- back, until one is chosen. The check's name is the message goose prints.
-- To list the rows: SELECT name, command, args FROM mcp_servers WHERE enabled = 1
-- AND (command = 'tether' OR command LIKE '%/tether') AND args LIKE '%--token-file%';
CREATE TEMP TABLE _tether_gateway_guard (
    n INTEGER CONSTRAINT "migration 182 refused: more than one enabled Tether gateway in mcp_servers (command tether with --token-file); keep one and disable or delete the others, then restart" CHECK (n <= 1)
);
INSERT INTO _tether_gateway_guard
SELECT COUNT(*) FROM mcp_servers
 WHERE enabled = 1
   AND (command = 'tether' OR command LIKE '%/tether')
   AND (args LIKE '%"--token-file"%' OR args LIKE '%"--token-file=%');
DROP TABLE _tether_gateway_guard;

UPDATE mcp_servers SET tether_gateway = 1
 WHERE enabled = 1
   AND (command = 'tether' OR command LIKE '%/tether')
   AND (args LIKE '%"--token-file"%' OR args LIKE '%"--token-file=%');

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_servers_one_tether_gateway
    ON mcp_servers(tether_gateway) WHERE tether_gateway = 1;

-- +goose Down
DROP INDEX IF EXISTS idx_mcp_servers_one_tether_gateway;
ALTER TABLE mcp_servers DROP COLUMN tether_gateway;
