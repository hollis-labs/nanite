-- +goose Up
-- Durable TeamRun launch and signal-resolution journals. A launch record is
-- written before the shared workflow host is entered, so a canceled caller or
-- process crash can replay the same keyed run and finish member projection.
CREATE TABLE team_run_launches (
    idempotency_key    TEXT PRIMARY KEY,
    team_id            TEXT NOT NULL REFERENCES teams(id),
    request_digest     TEXT NOT NULL,
    planning_json      TEXT NOT NULL,
    definition_json    TEXT NOT NULL DEFAULT '',
    launch_request_json TEXT NOT NULL DEFAULT '',
    members_json       TEXT NOT NULL DEFAULT '',
    workflow_run_id    TEXT REFERENCES workflow_runs(id),
    run_status         TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'prepared' CHECK (
        status IN ('planning','prepared','launched','members_ready','routing_ready')
    ),
    last_error         TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_team_run_launches_workflow_run
    ON team_run_launches(workflow_run_id)
    WHERE workflow_run_id IS NOT NULL;

CREATE INDEX idx_team_run_launches_recovery
    ON team_run_launches(status, updated_at, idempotency_key);

-- Member intents exist before session provisioning. Stable member/session
-- identities make a retry recover the same provisioning operation.
CREATE TABLE team_run_member_intents (
    idempotency_key TEXT NOT NULL,
    ordinal         INTEGER NOT NULL CHECK (ordinal >= 0),
    member_id       TEXT NOT NULL UNIQUE,
    team_id         TEXT NOT NULL REFERENCES teams(id),
    slot_name       TEXT NOT NULL,
    agent_id        TEXT NOT NULL REFERENCES agent_profiles(id),
    session_id      TEXT NOT NULL,
    provisioning_kind TEXT NOT NULL CHECK (provisioning_kind IN ('fresh','durable')),
    status          TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','provisioned')),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (idempotency_key, ordinal)
);

CREATE INDEX idx_team_run_member_intents_recovery
    ON team_run_member_intents(status, updated_at, idempotency_key, ordinal);

-- Resolution is prepared in the same transaction that stands down the active
-- phase members. If the host dies before closing the workflow wait, replay can
-- use this immutable outcome even though no active member remains.
CREATE TABLE team_signal_resolutions (
    workflow_run_id    TEXT NOT NULL REFERENCES workflow_runs(id),
    step_id            TEXT NOT NULL,
    responder_reference TEXT NOT NULL,
    output              TEXT NOT NULL,
    member_ids_json     TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'prepared' CHECK (
        status IN ('prepared','completed')
    ),
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    PRIMARY KEY (workflow_run_id, step_id)
);

CREATE INDEX idx_team_signal_resolutions_recovery
    ON team_signal_resolutions(status, updated_at, workflow_run_id, step_id);

-- +goose Down
DROP TABLE IF EXISTS team_signal_resolutions;
DROP TABLE IF EXISTS team_run_member_intents;
DROP TABLE IF EXISTS team_run_launches;
