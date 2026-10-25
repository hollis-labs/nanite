-- +goose Up
-- Enrollment record for an agent's durable Tether actor (CW-20261024-0017,
-- docs/architecture/tether-durable-actor.md). One row per host settings id.
-- The row is committed BEFORE Tether is called ('enrolling'), so a retry or a
-- restart after a crash between the Tether response and the local write finds
-- it and resumes; the registration is idempotent on external_key. It holds no
-- credential: the Tether registry token lives only in its own 0600 file.
--
-- status: enrolling -> enrolled -> deregister_pending -> retired.
-- deregister_pending means the actor is already archived and its binding
-- disabled locally and Tether has not yet confirmed the deregistration.
CREATE TABLE IF NOT EXISTS agent_enrollments (
    host_settings_id TEXT PRIMARY KEY REFERENCES agent_host_settings(id),
    external_key     TEXT NOT NULL UNIQUE,
    status           TEXT NOT NULL DEFAULT 'enrolling'
        CHECK (status IN ('enrolling','enrolled','deregister_pending','retired')),
    urn              TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT '',
    principal        TEXT NOT NULL CHECK (length(trim(principal)) > 0),
    attempts         INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    enrolled_at      TEXT NOT NULL DEFAULT '',
    retired_at       TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS agent_enrollments;
