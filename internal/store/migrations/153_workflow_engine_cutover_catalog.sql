-- +goose Up
-- 153_workflow_engine_cutover_catalog.sql
--
-- Durable control-plane state for cutting Agent Workflows from Nanite's
-- legacy sequencer to the extracted shared engine. Runtime code creates the
-- singleton cutover row when it is first needed; migrations remain schema
-- only. No legacy workflow is assigned a plan here because the exact authored
-- source may have moved, changed, or never existed on disk.

-- Keep both the pilot identity and the extracted release on the recovery hot
-- path during the one-way adoption window. The pilot-only index from 150 is
-- retained for rollback compatibility; this additive index is removed by Down.
CREATE INDEX idx_workflow_runs_embedded_runtime_recovery
    ON workflow_runs(runtime_status, started_at, id)
    WHERE engine_kind IN ('hadron_v0.5.0-beta.2','go_workflow_v0.1.0');

CREATE TABLE workflow_engine_cutovers (
    cutover_id                     TEXT PRIMARY KEY
        CHECK (cutover_id = 'agent_workflows'),
    phase                          TEXT NOT NULL
        CHECK (phase IN ('legacy','quiescing','shared_only')),
    generation                     INTEGER NOT NULL DEFAULT 0
        CHECK (generation >= 0),
    fence                          INTEGER NOT NULL DEFAULT 0
        CHECK (fence >= 0),
    target_engine_kind             TEXT NOT NULL
        CHECK (target_engine_kind = 'go_workflow_v0.1.0'),
    target_engine_contract_version TEXT NOT NULL
        CHECK (target_engine_contract_version = 'v0.1.0'),
    lease_owner                    TEXT NOT NULL DEFAULT '',
    lease_token                    TEXT NOT NULL DEFAULT '',
    lease_expires_at               TEXT NOT NULL DEFAULT '',
    updated_at                     TEXT NOT NULL,
    CHECK (
        (lease_owner = '' AND lease_token = '' AND lease_expires_at = '') OR
        (lease_owner <> '' AND lease_token <> '' AND lease_expires_at <> '')
    )
);

CREATE TABLE workflow_definition_revisions (
    revision_id                    TEXT PRIMARY KEY,
    definition_name                TEXT NOT NULL,
    source_locator                 TEXT NOT NULL,
    source_format                  TEXT NOT NULL,
    schema_version                 TEXT NOT NULL,
    source_digest                  TEXT NOT NULL,
    source_content                 BLOB NOT NULL CHECK (length(source_content) > 0),
    compiled_graph_digest          TEXT NOT NULL,
    compiled_plan_digest           TEXT NOT NULL,
    engine_kind                    TEXT NOT NULL,
    engine_contract_version        TEXT NOT NULL,
    registered_by                  TEXT NOT NULL,
    created_at                     TEXT NOT NULL,
    UNIQUE (definition_name, revision_id),
    FOREIGN KEY (compiled_plan_digest)
        REFERENCES workflow_plan_materials(plan_digest),
    CHECK (
        (engine_kind = 'hadron_v0.5.0-beta.2' AND
         engine_contract_version = 'v0.5.0-beta.2') OR
        (engine_kind = 'go_workflow_v0.1.0' AND
         engine_contract_version = 'v0.1.0')
    )
);

CREATE INDEX idx_workflow_definition_revisions_name_created
    ON workflow_definition_revisions(definition_name, created_at, revision_id);

-- +goose StatementBegin
CREATE TRIGGER workflow_definition_revisions_no_update
BEFORE UPDATE ON workflow_definition_revisions
BEGIN
    SELECT RAISE(ABORT, 'workflow definition revision is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_definition_revisions_no_delete
BEFORE DELETE ON workflow_definition_revisions
BEGIN
    SELECT RAISE(ABORT, 'workflow definition revision is immutable');
END;
-- +goose StatementEnd

CREATE TABLE workflow_definition_heads (
    definition_name TEXT PRIMARY KEY,
    revision_id     TEXT NOT NULL,
    generation      INTEGER NOT NULL CHECK (generation > 0),
    updated_at      TEXT NOT NULL,
    FOREIGN KEY (definition_name, revision_id)
        REFERENCES workflow_definition_revisions(definition_name, revision_id)
);

-- Selection may move between immutable revisions, but deleting the durable
-- selection would make a previously callable definition silently disappear.
-- +goose StatementBegin
CREATE TRIGGER workflow_definition_heads_no_delete
BEFORE DELETE ON workflow_definition_heads
BEGIN
    SELECT RAISE(ABORT, 'workflow definition head cannot be deleted');
END;
-- +goose StatementEnd

-- Selection changes are monotonic. The public API performs generation CAS;
-- this guard prevents out-of-band SQL from lowering or skipping generations.
-- +goose StatementBegin
CREATE TRIGGER workflow_definition_heads_generation_guard
BEFORE UPDATE ON workflow_definition_heads
WHEN NEW.definition_name <> OLD.definition_name OR
     NEW.generation <> OLD.generation + 1 OR
     NEW.revision_id = OLD.revision_id
BEGIN
    SELECT RAISE(ABORT, 'workflow definition head generation must advance exactly once');
END;
-- +goose StatementEnd

CREATE TABLE workflow_legacy_run_dispositions (
    cutover_id        TEXT NOT NULL,
    cutover_generation INTEGER NOT NULL CHECK (cutover_generation > 0),
    run_id            TEXT NOT NULL,
    observed_status   TEXT NOT NULL
        CHECK (observed_status IN ('running','waiting_on_gate','waiting_on_flex','waiting_on_loop')),
    disposition       TEXT NOT NULL DEFAULT 'pending'
        CHECK (disposition IN ('pending','drained','canceled','failed')),
    final_status      TEXT NOT NULL DEFAULT '',
    actor             TEXT NOT NULL DEFAULT '',
    reason            TEXT NOT NULL DEFAULT '',
    observed_at       TEXT NOT NULL,
    disposed_at       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (cutover_generation, run_id),
    FOREIGN KEY (cutover_id) REFERENCES workflow_engine_cutovers(cutover_id),
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id),
    CHECK (
        (disposition = 'pending' AND final_status = '' AND actor = '' AND
         reason = '' AND disposed_at = '') OR
        (disposition <> 'pending' AND
         final_status IN ('completed','failed','canceled') AND
         actor <> '' AND reason <> '' AND disposed_at <> '')
    )
);

CREATE INDEX idx_workflow_legacy_dispositions_pending
    ON workflow_legacy_run_dispositions(
        cutover_id, cutover_generation, disposition, run_id
    );

-- A pending observation may be completed exactly once. Once disposed, the
-- audit record is append-only in effect and cannot be rewritten.
-- +goose StatementBegin
CREATE TRIGGER workflow_legacy_dispositions_final_no_update
BEFORE UPDATE ON workflow_legacy_run_dispositions
WHEN OLD.disposition <> 'pending'
BEGIN
    SELECT RAISE(ABORT, 'workflow legacy disposition is final');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_legacy_dispositions_identity_no_update
BEFORE UPDATE ON workflow_legacy_run_dispositions
WHEN NEW.cutover_id <> OLD.cutover_id OR
     NEW.cutover_generation <> OLD.cutover_generation OR
     NEW.run_id <> OLD.run_id OR
     NEW.observed_status <> OLD.observed_status OR
     NEW.observed_at <> OLD.observed_at
BEGIN
    SELECT RAISE(ABORT, 'workflow legacy disposition identity is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_legacy_dispositions_no_delete
BEFORE DELETE ON workflow_legacy_run_dispositions
BEGIN
    SELECT RAISE(ABORT, 'workflow legacy disposition audit cannot be deleted');
END;
-- +goose StatementEnd

-- +goose Down

DROP INDEX IF EXISTS idx_workflow_runs_embedded_runtime_recovery;
DROP TRIGGER IF EXISTS workflow_legacy_dispositions_no_delete;
DROP TRIGGER IF EXISTS workflow_legacy_dispositions_identity_no_update;
DROP TRIGGER IF EXISTS workflow_legacy_dispositions_final_no_update;
DROP TABLE IF EXISTS workflow_legacy_run_dispositions;
DROP TRIGGER IF EXISTS workflow_definition_heads_no_delete;
DROP TRIGGER IF EXISTS workflow_definition_heads_generation_guard;
DROP TABLE IF EXISTS workflow_definition_heads;
DROP TRIGGER IF EXISTS workflow_definition_revisions_no_delete;
DROP TRIGGER IF EXISTS workflow_definition_revisions_no_update;
DROP TABLE IF EXISTS workflow_definition_revisions;
DROP TABLE IF EXISTS workflow_engine_cutovers;
