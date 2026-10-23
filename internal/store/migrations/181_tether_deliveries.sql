-- +goose Up
-- Idempotency ledger for Tether deliveries (CW-20261022-0002). One row per
-- Tether message id, written the moment the bridge first sees an envelope and
-- before it claims or injects anything, so a redelivery, lease expiry or
-- restart finds the row and resumes rather than starting a second turn.
-- envelope_json is cleared once a delivery is terminal. next_attempt_at defers
-- a row (busy, unbound, backoff); empty means due now.
-- state: received -> accepted -> submitted -> consumed | failed.
CREATE TABLE IF NOT EXISTS tether_deliveries (
    message_id       TEXT PRIMARY KEY,
    actor_urn        TEXT NOT NULL,
    local_message_id TEXT NOT NULL DEFAULT '',
    envelope_json    TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'received',
    attempts         INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    next_attempt_at  TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tether_deliveries_state ON tether_deliveries(state);

-- +goose Down
DROP INDEX IF EXISTS idx_tether_deliveries_state;
DROP TABLE IF EXISTS tether_deliveries;
