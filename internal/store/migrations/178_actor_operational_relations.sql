-- +goose Up

-- New actor operational relations. No historical values or authority are copied.

-- Legacy graph remains solely for explicit historical export/retirement.
-- Procedures, knowledge seeds, mutable reflex rules/resolvers and legacy grant
-- backfill have no fresh equivalents. Pinned content owns intrinsic behavior.

CREATE TABLE actor_known_tools (
    agent_id         TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    tool_name        TEXT NOT NULL,
    pinned           INTEGER NOT NULL DEFAULT 0,
    activation_count INTEGER NOT NULL DEFAULT 0,
    last_used_at     TEXT,
    added_at         TEXT NOT NULL DEFAULT (datetime('now')),
    ttl_seconds      INTEGER,
    reason           TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (agent_id, tool_name)
);

-- +goose StatementBegin

CREATE TRIGGER actor_known_tools_requires_binding BEFORE INSERT ON actor_known_tools
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_known_tools_update_requires_binding BEFORE UPDATE ON actor_known_tools
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE actor_known_skills (
    agent_id         TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    skill_name       TEXT NOT NULL,
    pinned           INTEGER NOT NULL DEFAULT 0,
    activation_count INTEGER NOT NULL DEFAULT 0,
    last_used_at     TEXT,
    added_at         TEXT NOT NULL DEFAULT (datetime('now')),
    ttl_seconds      INTEGER,
    reason           TEXT NOT NULL DEFAULT '', approved_content_hash TEXT, granted_at TEXT, granted_by TEXT, capabilities_granted TEXT,
    PRIMARY KEY (agent_id, skill_name)
);

-- +goose StatementBegin

CREATE TRIGGER actor_known_skills_requires_binding BEFORE INSERT ON actor_known_skills
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_known_skills_update_requires_binding BEFORE UPDATE ON actor_known_skills
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
AND NOT (
 NEW.agent_id IS OLD.agent_id AND NEW.skill_name IS OLD.skill_name
 AND NEW.pinned IS OLD.pinned AND NEW.activation_count IS OLD.activation_count
 AND NEW.last_used_at IS OLD.last_used_at AND NEW.added_at IS OLD.added_at
 AND NEW.ttl_seconds IS OLD.ttl_seconds AND NEW.reason IS OLD.reason
 AND COALESCE(NEW.approved_content_hash,'')='' AND COALESCE(NEW.granted_at,'')=''
 AND COALESCE(NEW.granted_by,'')='' AND COALESCE(NEW.capabilities_granted,'')=''
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE actor_log (
    id         TEXT PRIMARY KEY,
    agent_id   TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    session_id TEXT NOT NULL DEFAULT '',
    ts         TEXT NOT NULL DEFAULT (datetime('now')),
    kind       TEXT NOT NULL DEFAULT '',
    entry      TEXT NOT NULL DEFAULT ''
);

-- +goose StatementBegin

CREATE TRIGGER actor_log_requires_binding BEFORE INSERT ON actor_log
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_log_update_requires_binding BEFORE UPDATE ON actor_log
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE "actor_instances" (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    slug               TEXT NOT NULL UNIQUE,
    profile_id         TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    lifecycle_class    TEXT NOT NULL DEFAULT 'advisor'
        CHECK(lifecycle_class IN ('advisor','process','template','harness')),
    provider           TEXT NOT NULL DEFAULT '',
    model              TEXT NOT NULL DEFAULT '',
    runtime_kind       TEXT NOT NULL DEFAULT 'api',
    launch_source_type TEXT NOT NULL DEFAULT 'durable_advisor'
        CHECK(launch_source_type IN ('api_chat','cli_harness','boot_profile','durable_advisor','process_tick','task_template_run')),
    launch_source_id   TEXT NOT NULL DEFAULT '',
    work_root          TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'sleeping'
        CHECK(status IN ('sleeping','starting','active','paused','stopped','start_requested','stop_requested','resume_requested','failed','archived')),
    current_session_id TEXT NOT NULL DEFAULT '',
    failure_reason     TEXT NOT NULL DEFAULT '',
    metadata_json      TEXT NOT NULL DEFAULT '{}',
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),
    archived_at        TEXT
, urn TEXT NOT NULL DEFAULT '');

-- +goose StatementBegin

CREATE TRIGGER actor_instances_requires_binding BEFORE INSERT ON actor_instances
WHEN NEW.profile_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.profile_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_instances_update_requires_binding BEFORE UPDATE ON actor_instances
WHEN NEW.profile_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.profile_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE "actor_projects" (
    agent_id TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, project_id)
);

-- +goose StatementBegin

CREATE TRIGGER actor_projects_requires_binding BEFORE INSERT ON actor_projects
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_projects_update_requires_binding BEFORE UPDATE ON actor_projects
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE actor_granted_tools (
    agent_id    TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri) ON DELETE CASCADE,
    tool_id     TEXT NOT NULL REFERENCES known_tools(id) ON DELETE CASCADE,
    granted_via TEXT NOT NULL DEFAULT 'explicit',
    created_at  TEXT NOT NULL,
    PRIMARY KEY (agent_id, tool_id)
);

-- +goose StatementBegin

CREATE TRIGGER actor_granted_tools_requires_binding BEFORE INSERT ON actor_granted_tools
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_granted_tools_update_requires_binding BEFORE UPDATE ON actor_granted_tools
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE actor_dispatch_tool_allowlist (
    agent_id   TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri) ON DELETE CASCADE,
    tool_id    TEXT NOT NULL REFERENCES known_tools(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (agent_id, tool_id)
);

-- +goose StatementBegin

CREATE TRIGGER actor_dispatch_tool_allowlist_requires_binding BEFORE INSERT ON actor_dispatch_tool_allowlist
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_dispatch_tool_allowlist_update_requires_binding BEFORE UPDATE ON actor_dispatch_tool_allowlist
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE actor_team_run_members (
    id                TEXT PRIMARY KEY,
    workflow_run_id   TEXT NOT NULL REFERENCES workflow_runs(id),
    slot_name         TEXT NOT NULL,
    agent_id          TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    session_id        TEXT NOT NULL REFERENCES sessions(id),
    resolved_at       TEXT NOT NULL DEFAULT (datetime('now')),
    status            TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','failed','replaced','stopped')
    )
);

-- +goose StatementBegin

CREATE TRIGGER actor_team_run_members_requires_binding BEFORE INSERT ON actor_team_run_members
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_team_run_members_update_requires_binding BEFORE UPDATE ON actor_team_run_members
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE "actor_schedules" (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    session_id      TEXT,
    name            TEXT NOT NULL,
    schedule_kind   TEXT NOT NULL CHECK (
        schedule_kind IN ('cron','one_shot')
    ),
    schedule_spec   TEXT NOT NULL DEFAULT '',
    body            TEXT NOT NULL,
    priority        INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (
        status IN ('active','paused','expired')
    ),
    expires_at      TEXT,
    fired_count     INTEGER NOT NULL DEFAULT 0,
    last_fired_at   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    created_by      TEXT NOT NULL DEFAULT 'operator',
    max_retries     INTEGER NOT NULL DEFAULT 3,
    on_fail         TEXT NOT NULL DEFAULT 'retry' CHECK (
        on_fail IN ('retry','disable','notify')
    ),
    next_run        TEXT,
    job_type        TEXT NOT NULL DEFAULT 'durable_agent_wake' CHECK (
        job_type IN ('durable_agent_wake','agent_workflow_run','command_run','reflex_dispatch','loop_run_tick')
    ),
    job_payload     TEXT NOT NULL DEFAULT '{}'
);

-- +goose StatementBegin

CREATE TRIGGER actor_schedules_requires_binding BEFORE INSERT ON actor_schedules
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_schedules_update_requires_binding BEFORE UPDATE ON actor_schedules
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE actor_team_run_member_intents (
    idempotency_key TEXT NOT NULL,
    ordinal         INTEGER NOT NULL CHECK (ordinal >= 0),
    member_id       TEXT NOT NULL UNIQUE,
    team_id         TEXT NOT NULL REFERENCES teams(id),
    slot_name       TEXT NOT NULL,
    agent_id        TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    session_id      TEXT NOT NULL,
    provisioning_kind TEXT NOT NULL CHECK (provisioning_kind IN ('fresh','durable')),
    status          TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','provisioned')),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (idempotency_key, ordinal)
);

-- +goose StatementBegin

CREATE TRIGGER actor_team_run_member_intents_requires_binding BEFORE INSERT ON actor_team_run_member_intents
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER actor_team_run_member_intents_update_requires_binding BEFORE UPDATE ON actor_team_run_member_intents
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

CREATE TABLE session_actor_bindings (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    agent_id TEXT NOT NULL REFERENCES agent_actor_bindings(actor_uri),
    mode TEXT DEFAULT 'default',
    joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_primary BOOLEAN DEFAULT FALSE,
    PRIMARY KEY (session_id, agent_id)
);

-- +goose StatementBegin

CREATE TRIGGER session_actor_bindings_requires_binding BEFORE INSERT ON session_actor_bindings
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
)
BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd

-- +goose StatementBegin

CREATE TRIGGER session_actor_bindings_update_requires_binding BEFORE UPDATE ON session_actor_bindings
WHEN NEW.agent_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id
 WHERE b.actor_uri=NEW.agent_id AND b.enabled=1 AND h.enabled=1 AND length(trim(b.binding_receipt))>0
) AND NOT (NEW.is_primary=FALSE AND OLD.is_primary=TRUE AND NEW.session_id=OLD.session_id AND NEW.agent_id=OLD.agent_id AND NEW.mode IS OLD.mode AND NEW.joined_at=OLD.joined_at)

BEGIN SELECT RAISE(ABORT,'verified enabled actor binding required'); END;

-- +goose StatementEnd


-- Operational children reference the fresh actor graph, never historical parents.
CREATE TABLE "actor_instance_sessions" (
    instance_id TEXT NOT NULL REFERENCES actor_instances(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    relation    TEXT NOT NULL DEFAULT 'owned'
        CHECK(relation IN ('owned','attached','spawned','primary','run','wake','harness')),
    attached_at TEXT NOT NULL DEFAULT (datetime('now')),
    detached_at TEXT,
    PRIMARY KEY(instance_id, session_id)
);

CREATE TABLE actor_instance_events (
    id              TEXT PRIMARY KEY,
    instance_id     TEXT NOT NULL REFERENCES actor_instances(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    status_before   TEXT NOT NULL DEFAULT '',
    status_after    TEXT NOT NULL DEFAULT '',
    session_id      TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL DEFAULT 'api',
    message         TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE "actor_schedule_runs" (
    id                       TEXT PRIMARY KEY,
    schedule_id              TEXT NOT NULL REFERENCES actor_schedules(id),
    run_id                   TEXT NOT NULL,
    scheduled_at             TEXT NOT NULL,
    fired_at                 TEXT NOT NULL,
    claim_expires_at         TEXT,
    dispatch_accepted_at     TEXT,
    status                   TEXT NOT NULL CHECK (
        status IN ('pending','claimed','retrying','succeeded','skipped','exhausted')
    ),
    attempt_count            INTEGER NOT NULL DEFAULT 0,
    last_error               TEXT,
    next_attempt_at          TEXT,
    retry_max_attempts       INTEGER NOT NULL DEFAULT 0,
    retry_backoff_strategy   TEXT NOT NULL DEFAULT 'none' CHECK (
        retry_backoff_strategy IN ('none','constant','linear','exponential')
    ),
    retry_initial_delay_ns   INTEGER NOT NULL DEFAULT 0,
    retry_max_delay_ns       INTEGER NOT NULL DEFAULT 0,
    job_type                 TEXT NOT NULL,
    job_payload              TEXT NOT NULL
);

-- +goose Down

DROP TABLE actor_schedule_runs;
DROP TABLE actor_instance_events;
DROP TABLE actor_instance_sessions;

DROP TABLE session_actor_bindings;

DROP TABLE actor_team_run_member_intents;

DROP TABLE actor_schedules;

DROP TABLE actor_team_run_members;

DROP TABLE actor_dispatch_tool_allowlist;

DROP TABLE actor_granted_tools;

DROP TABLE actor_projects;

DROP TABLE actor_instances;

DROP TABLE actor_log;

DROP TABLE actor_known_skills;

DROP TABLE actor_known_tools;
