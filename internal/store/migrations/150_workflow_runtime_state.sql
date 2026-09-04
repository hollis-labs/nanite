-- +goose Up
-- 150_workflow_runtime_state.sql
-- Nanite's production SQLite implementation of Hadron workflow/runtime.
-- The historical Hadron migrations are squashed into their final schema shape.
-- workflow_runs remains Nanite's sole run identity and carries both canonical
-- runtime fields and the existing product compatibility projection.

-- ---- Adapted from Hadron 0014_workflow_state.sql at cfce3b0 ----
-- Graph-native workflow runtime state. These tables are intentionally separate
-- from the legacy blueprint and pipeline execution tables.

CREATE TABLE workflow_plan_refs (
    digest TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    version TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    plan_snapshot_json TEXT,
    source_map_json TEXT,
    source_snapshot_json TEXT
);

CREATE UNIQUE INDEX idx_workflow_plan_refs_identity
    ON workflow_plan_refs(plan_id, version, digest);

-- Nanite already owns workflow_runs as its product run identity. Add canonical
-- runtime fields to that row so there is no mirrored second run database.
ALTER TABLE workflow_runs ADD COLUMN engine_kind TEXT NOT NULL DEFAULT 'nanite_builtin_v1';
ALTER TABLE workflow_runs ADD COLUMN engine_contract_version TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_runs ADD COLUMN plan_digest TEXT REFERENCES workflow_plan_refs(digest);
ALTER TABLE workflow_runs ADD COLUMN runtime_status TEXT;
ALTER TABLE workflow_runs ADD COLUMN inputs_ref_json TEXT;
ALTER TABLE workflow_runs ADD COLUMN outputs_ref_json TEXT;
ALTER TABLE workflow_runs ADD COLUMN runtime_generation INTEGER NOT NULL DEFAULT 0
    CHECK (runtime_generation >= 0);
ALTER TABLE workflow_runs ADD COLUMN runtime_failure_json TEXT;

CREATE INDEX idx_workflow_runs_runtime_recovery
    ON workflow_runs(runtime_status, started_at, id)
    WHERE engine_kind = 'hadron_v0.5.0-beta.2';

CREATE TABLE workflow_run_start_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

CREATE TABLE workflow_node_invocations (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    blocked_json TEXT,
    inputs_ref_json TEXT,
    outputs_ref_json TEXT,
    wait_id TEXT,
    latest_attempt INTEGER NOT NULL DEFAULT 0 CHECK (latest_attempt >= 0),
    priority INTEGER NOT NULL DEFAULT 0,
    claim_generation INTEGER NOT NULL DEFAULT 0 CHECK (claim_generation >= 0),
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration),
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id)
);

CREATE INDEX idx_workflow_nodes_recovery
    ON workflow_node_invocations(status, priority DESC, created_at, run_id, node_id, iteration);

CREATE TABLE workflow_node_leases (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    owner TEXT NOT NULL,
    token TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    expires_at TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
        ON DELETE CASCADE
);

CREATE INDEX idx_workflow_node_leases_expiry
    ON workflow_node_leases(expires_at, run_id, node_id, iteration);

CREATE TABLE workflow_claim_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

CREATE TABLE workflow_attempts (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    status TEXT NOT NULL,
    executor_json TEXT NOT NULL,
    inputs_ref_json TEXT,
    outputs_ref_json TEXT,
    failure_json TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration, attempt_number),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
        ON DELETE CASCADE
);

CREATE INDEX idx_workflow_attempts_invocation
    ON workflow_attempts(run_id, node_id, iteration, attempt_number);

CREATE TABLE workflow_waits (
    wait_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    resume_values_ref_json TEXT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    resolved_at TEXT,
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

CREATE INDEX idx_workflow_waits_recovery
    ON workflow_waits(status, updated_at, wait_id);

CREATE TABLE workflow_wait_resume_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

CREATE TABLE workflow_value_sets (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    digest TEXT NOT NULL,
    owner_json TEXT NOT NULL,
    values_json TEXT NOT NULL
);

CREATE INDEX idx_workflow_value_sets_digest
    ON workflow_value_sets(digest, sequence);

CREATE TABLE workflow_event_sequences (
    run_id TEXT PRIMARY KEY,
    last_sequence INTEGER NOT NULL CHECK (last_sequence >= 0)
);

CREATE TABLE workflow_events (
    run_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    invocation_json TEXT,
    attempt_json TEXT,
    event_type TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    attributes_json TEXT,
    values_ref_json TEXT,
    redaction TEXT NOT NULL,
    retention TEXT NOT NULL,
    PRIMARY KEY (run_id, sequence)
);

CREATE INDEX idx_workflow_events_type
    ON workflow_events(event_type, occurred_at, run_id, sequence);

-- +goose StatementBegin
CREATE TRIGGER workflow_events_reject_update
BEFORE UPDATE ON workflow_events
BEGIN
    SELECT RAISE(ABORT, 'workflow events are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_events_reject_delete
BEFORE DELETE ON workflow_events
BEGIN
    SELECT RAISE(ABORT, 'workflow events are append-only');
END;
-- +goose StatementEnd

CREATE TABLE workflow_cache_entries (
    cache_key TEXT PRIMARY KEY,
    plan_digest TEXT NOT NULL,
    node_id TEXT NOT NULL,
    input_digest TEXT NOT NULL,
    outputs_ref_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT
);

CREATE INDEX idx_workflow_cache_expiry
    ON workflow_cache_entries(expires_at, cache_key);

CREATE TABLE workflow_pinned_values (
    pin_key TEXT PRIMARY KEY,
    value_ref_json TEXT NOT NULL,
    pinned_at TEXT NOT NULL,
    expires_at TEXT
);

CREATE INDEX idx_workflow_pins_expiry
    ON workflow_pinned_values(expires_at, pin_key);

CREATE TABLE workflow_external_activations (
    idempotency_key TEXT PRIMARY KEY,
    activation_id TEXT NOT NULL,
    requested_run_id TEXT NOT NULL,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

CREATE INDEX idx_workflow_activations_run
    ON workflow_external_activations(requested_run_id, activation_id);

CREATE INDEX idx_workflow_activations_registration
    ON workflow_external_activations(activation_id, idempotency_key);

-- ---- Adapted from Hadron 0015_workflow_generic_waits.sql at cfce3b0 ----
ALTER TABLE workflow_waits ADD COLUMN record_json TEXT;
ALTER TABLE workflow_waits ADD COLUMN deadline TEXT;

-- Rows created by migration 0014 did not persist correlation, authority, or a
-- resume-token capability. They remain readable and timeout/cancel history is
-- preserved, but open rows are deliberately unresumable: the migration marks
-- their authority accordingly and assigns a token digest for which no raw
-- token was ever issued.
UPDATE workflow_waits
SET deadline = CASE WHEN status = 'timed_out' THEN resolved_at ELSE NULL END,
    record_json = json_object(
        'kind', CASE WHEN status = 'timed_out' THEN 'timer' ELSE 'signal' END,
        'correlation', wait_id,
        'deadline', CASE WHEN status = 'timed_out' THEN resolved_at ELSE NULL END,
        'resume_schema', json_object(
            'digest', 'sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a',
            'schema', json('{}')
        ),
        'resume_token_digest', 'sha256:e19e9ffb88b17165916bcb255875c04c023f664d2a1cf235b95b68199ebfa54e',
        'visibility', 'private',
        'authority', json_object('kind', 'legacy_unresumable', 'reference', 'migration-0015'),
        'wake_source', CASE WHEN status = 'timed_out' THEN 'timer' ELSE 'signal' END,
        'status', status,
        'resume_values', CASE WHEN resume_values_ref_json IS NULL THEN NULL ELSE json(resume_values_ref_json) END,
        'resolution', CASE
            WHEN status = 'resumed' THEN json_object(
                'source', 'signal',
                'responder', json_object('kind', 'legacy', 'reference', 'migration-0015'),
                'payload_digest', CASE WHEN resume_values_ref_json IS NULL THEN '' ELSE json_extract(resume_values_ref_json, '$.digest') END,
                'resolved_at', resolved_at
            )
            WHEN status = 'timed_out' THEN json_object(
                'source', 'timer',
                'responder', json_object('kind', 'system', 'reference', 'wait-timeout'),
                'resolved_at', resolved_at
            )
            WHEN status = 'canceled' THEN json_object(
                'source', 'signal',
                'responder', json_object('kind', 'system', 'reference', 'migration-0015'),
                'resolved_at', resolved_at
            )
            ELSE NULL
        END
    );

CREATE INDEX idx_workflow_waits_deadline
    ON workflow_waits(status, deadline, created_at, wait_id);

CREATE INDEX idx_workflow_waits_correlation
    ON workflow_waits(status, json_extract(record_json, '$.correlation'), wait_id);

-- Wait identity is the suspension idempotency identity. The table retains the
-- canonical request and complete atomic result for exact retries.
CREATE TABLE workflow_wait_suspend_idempotency (
    wait_id TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

CREATE TABLE workflow_wait_resume_results (
    wait_id TEXT PRIMARY KEY,
    result_json TEXT NOT NULL
);

CREATE TABLE workflow_wait_timeout_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

-- ---- Adapted from Hadron 0016_workflow_step_runtime.sql at cfce3b0 ----
-- Bind generic waits to the exact unfinished attempt they suspend. The unique
-- attempt key intentionally defines one generic wait per logical attempt;
-- multi-stage suspension requires a new attempt rather than timestamp-based
-- continuation inference.
CREATE TABLE workflow_wait_attempt_bindings (
    wait_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    UNIQUE (run_id, node_id, iteration, attempt_number),
    FOREIGN KEY (wait_id) REFERENCES workflow_waits(wait_id),
    FOREIGN KEY (run_id, node_id, iteration, attempt_number)
        REFERENCES workflow_attempts(run_id, node_id, iteration, attempt_number)
);

CREATE INDEX idx_workflow_wait_attempt_lookup
    ON workflow_wait_attempt_bindings(run_id, node_id, iteration, attempt_number, wait_id);

-- Backfill only a current, payload-safe 0015 wait whose node timestamp and
-- lifecycle state prove it belongs to the node's latest unfinished attempt.
-- Legacy-unresumable rows remain readable but intentionally unbound.
INSERT INTO workflow_wait_attempt_bindings(wait_id, run_id, node_id, iteration, attempt_number)
SELECT w.wait_id, w.run_id, w.node_id, w.iteration, n.latest_attempt
FROM workflow_waits w
JOIN workflow_node_invocations n
  ON n.run_id = w.run_id AND n.node_id = w.node_id AND n.iteration = w.iteration
JOIN workflow_attempts a
  ON a.run_id = n.run_id AND a.node_id = n.node_id AND a.iteration = n.iteration
 AND a.attempt_number = n.latest_attempt
WHERE a.status = 'running'
  AND w.updated_at = n.updated_at
  AND ((w.status = 'open' AND n.status = 'waiting')
    OR (w.status = 'resumed' AND n.status = 'ready'))
  AND COALESCE(json_extract(w.record_json, '$.authority.kind'), '') <> 'legacy_unresumable'
  AND (
      SELECT COUNT(*) FROM workflow_waits candidate
      WHERE candidate.run_id = w.run_id AND candidate.node_id = w.node_id
        AND candidate.iteration = w.iteration
        AND candidate.updated_at = n.updated_at
        AND ((candidate.status = 'open' AND n.status = 'waiting')
          OR (candidate.status = 'resumed' AND n.status = 'ready'))
        AND COALESCE(json_extract(candidate.record_json, '$.authority.kind'), '') <> 'legacy_unresumable'
  ) = 1;

-- One immutable adapter-owned operation is bound to one exact attempt. Mutable
-- observation fields advance only through generation-CAS runtime operations.
CREATE TABLE workflow_external_operations (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    ref_json TEXT NOT NULL,
    invocation_json TEXT NOT NULL,
    status TEXT NOT NULL,
    progress_json TEXT,
    outputs_ref_json TEXT,
    failure_json TEXT,
    cancel_requested_at TEXT,
    last_observed_at TEXT,
    last_heartbeat_at TEXT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration, attempt_number),
    FOREIGN KEY (run_id, node_id, iteration, attempt_number)
        REFERENCES workflow_attempts(run_id, node_id, iteration, attempt_number)
);

CREATE INDEX idx_workflow_external_operations_recovery
    ON workflow_external_operations(status, updated_at, run_id, node_id, iteration, attempt_number);

CREATE INDEX idx_workflow_external_operations_cancel
    ON workflow_external_operations(status, cancel_requested_at, updated_at);

-- +goose StatementBegin
CREATE TRIGGER workflow_external_operations_immutable_binding
BEFORE UPDATE ON workflow_external_operations
WHEN OLD.run_id <> NEW.run_id
  OR OLD.node_id <> NEW.node_id
  OR OLD.iteration <> NEW.iteration
  OR OLD.attempt_number <> NEW.attempt_number
  OR OLD.ref_json <> NEW.ref_json
  OR OLD.invocation_json <> NEW.invocation_json
BEGIN
    SELECT RAISE(ABORT, 'workflow external operation binding is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_external_operations_reject_delete
BEFORE DELETE ON workflow_external_operations
BEGIN
    SELECT RAISE(ABORT, 'workflow external operations are durable history');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0017_workflow_retry_cancel_fanout.sql at cfce3b0 ----
-- Restart-durable retry timers, run-cancellation work, and runtime fan-out.
-- These graph-native tables remain separate from legacy pipeline persistence.

CREATE TABLE workflow_retry_activations (
    activation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    status TEXT NOT NULL,
    fire_at TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    snapshot_json TEXT NOT NULL,
    UNIQUE (run_id, node_id, iteration, attempt_number),
    FOREIGN KEY (run_id, node_id, iteration, attempt_number)
        REFERENCES workflow_attempts(run_id, node_id, iteration, attempt_number)
);

CREATE INDEX idx_workflow_retry_activations_recovery
    ON workflow_retry_activations(status, fire_at, activation_id);

CREATE TABLE workflow_retry_activation_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

CREATE TABLE workflow_fanouts (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '' CHECK (iteration = ''),
    status TEXT NOT NULL,
    max_concurrency INTEGER NOT NULL CHECK (max_concurrency >= 0),
    generation INTEGER NOT NULL CHECK (generation > 0),
    snapshot_json TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

CREATE INDEX idx_workflow_fanouts_status
    ON workflow_fanouts(status, run_id, node_id);

CREATE TABLE workflow_fanout_items (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    item_index INTEGER NOT NULL CHECK (item_index >= 0),
    iteration TEXT NOT NULL,
    inputs_ref_json TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, item_index),
    UNIQUE (run_id, node_id, iteration),
    FOREIGN KEY (run_id, node_id)
        REFERENCES workflow_fanouts(run_id, node_id),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

CREATE INDEX idx_workflow_fanout_items_invocation
    ON workflow_fanout_items(run_id, node_id, iteration, item_index);

CREATE TABLE workflow_child_runs (
    parent_run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    child_run_id TEXT NOT NULL,
    policy TEXT NOT NULL,
    created_at TEXT NOT NULL,
    link_json TEXT NOT NULL,
    PRIMARY KEY (parent_run_id, node_id, iteration),
    UNIQUE (child_run_id),
    FOREIGN KEY (parent_run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration),
    FOREIGN KEY (child_run_id) REFERENCES workflow_runs(id)
);

CREATE INDEX idx_workflow_child_runs_parent
    ON workflow_child_runs(parent_run_id, child_run_id);

CREATE TABLE workflow_cancellation_intents (
    intent_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    snapshot_json TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id)
);

CREATE INDEX idx_workflow_cancellation_intents_recovery
    ON workflow_cancellation_intents(status, requested_at, intent_id);

CREATE TABLE workflow_run_cancellation_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

-- +goose StatementBegin
CREATE TRIGGER workflow_retry_activations_immutable_attempt
BEFORE UPDATE ON workflow_retry_activations
WHEN OLD.run_id <> NEW.run_id
  OR OLD.node_id <> NEW.node_id
  OR OLD.iteration <> NEW.iteration
  OR OLD.attempt_number <> NEW.attempt_number
BEGIN
    SELECT RAISE(ABORT, 'workflow retry activation attempt is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_fanout_items_immutable
BEFORE UPDATE ON workflow_fanout_items
BEGIN
    SELECT RAISE(ABORT, 'workflow fan-out item bindings are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_fanout_items_reject_delete
BEFORE DELETE ON workflow_fanout_items
BEGIN
    SELECT RAISE(ABORT, 'workflow fan-out item bindings are durable history');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_child_runs_immutable
BEFORE UPDATE ON workflow_child_runs
BEGIN
    SELECT RAISE(ABORT, 'workflow child run links are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_child_runs_reject_delete
BEFORE DELETE ON workflow_child_runs
BEGIN
    SELECT RAISE(ABORT, 'workflow child run links are durable history');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0019_workflow_control_flow.sql at cfce3b0 ----
-- Durable graph-native switch/catch decisions and run terminal intents.
-- Typed errors remain in workflow_value_sets and are referenced by sequence;
-- all mutations are coordinated by WorkflowStateStore transactions.

CREATE TABLE workflow_control_decisions (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    outcome TEXT NOT NULL,
    source_generation INTEGER NOT NULL CHECK (source_generation > 0),
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_at TEXT NOT NULL,
    error_values_sequence INTEGER,
    snapshot_json TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration, kind),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration),
    FOREIGN KEY (error_values_sequence) REFERENCES workflow_value_sets(sequence)
);

CREATE INDEX idx_workflow_control_decisions_run
    ON workflow_control_decisions(run_id, node_id, iteration, kind);

CREATE TABLE workflow_terminal_intents (
    run_id TEXT PRIMARY KEY,
    intended_status TEXT NOT NULL,
    status TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT,
    error_values_sequence INTEGER,
    immutable_json TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id),
    FOREIGN KEY (error_values_sequence) REFERENCES workflow_value_sets(sequence)
);

CREATE INDEX idx_workflow_terminal_intents_recovery
    ON workflow_terminal_intents(status, updated_at, run_id);

-- The immutable canonical request proves that one atomic cancellation covered
-- the exact locally owned ParentCloseCancel descendant closure. The legacy
-- cancellation idempotency row remains the root-request replay contract used
-- by WorkflowHostStore.
CREATE TABLE workflow_control_cancellation_trees (
    root_run_id TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (root_run_id) REFERENCES workflow_runs(id)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_control_decisions_reject_update
BEFORE UPDATE ON workflow_control_decisions BEGIN
    SELECT RAISE(ABORT, 'workflow control decisions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_control_decisions_reject_delete
BEFORE DELETE ON workflow_control_decisions BEGIN
    SELECT RAISE(ABORT, 'workflow control decisions are durable history');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_terminal_intents_immutable_fields
BEFORE UPDATE ON workflow_terminal_intents
WHEN OLD.run_id <> NEW.run_id
  OR OLD.intended_status <> NEW.intended_status
  OR OLD.idempotency_key <> NEW.idempotency_key
  OR OLD.created_at <> NEW.created_at
  OR COALESCE(OLD.error_values_sequence, -1) <> COALESCE(NEW.error_values_sequence, -1)
  OR OLD.immutable_json <> NEW.immutable_json
BEGIN
    SELECT RAISE(ABORT, 'workflow terminal intent identity is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_terminal_intents_valid_transition
BEFORE UPDATE ON workflow_terminal_intents
WHEN OLD.status <> 'pending'
  OR NEW.status <> 'completed'
  OR NEW.generation <> OLD.generation + 1
  OR NEW.completed_at IS NULL
  OR NEW.completed_at <> NEW.updated_at
BEGIN
    SELECT RAISE(ABORT, 'workflow terminal intent transition is invalid');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_terminal_intents_reject_delete
BEFORE DELETE ON workflow_terminal_intents BEGIN
    SELECT RAISE(ABORT, 'workflow terminal intents are durable history');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_control_cancellation_trees_reject_update
BEFORE UPDATE ON workflow_control_cancellation_trees BEGIN
    SELECT RAISE(ABORT, 'workflow control cancellation trees are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_control_cancellation_trees_reject_delete
BEFORE DELETE ON workflow_control_cancellation_trees BEGIN
    SELECT RAISE(ABORT, 'workflow control cancellation trees are durable history');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0020_workflow_scheduler_resources.sql at cfce3b0 ----
-- Cross-process scheduler resource definitions, lease-fenced holders, and
-- diagnostic waiters. Capacity ownership is committed atomically with the
-- workflow node claim that supplies claim_generation and lease expiry.

CREATE TABLE workflow_scheduler_resources (
    resource_key TEXT PRIMARY KEY,
    limit_value INTEGER NOT NULL CHECK (limit_value > 0),
    resource_json TEXT NOT NULL
);

CREATE TABLE workflow_scheduler_holders (
    resource_key TEXT NOT NULL,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    units INTEGER NOT NULL CHECK (units > 0),
    claim_generation INTEGER NOT NULL CHECK (claim_generation > 0),
    owner TEXT NOT NULL,
    acquired_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    PRIMARY KEY (resource_key, run_id, node_id, iteration),
    FOREIGN KEY (resource_key) REFERENCES workflow_scheduler_resources(resource_key),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

CREATE INDEX idx_workflow_scheduler_holders_capacity
    ON workflow_scheduler_holders(resource_key, expires_at, run_id, node_id, iteration);

CREATE INDEX idx_workflow_scheduler_holders_invocation
    ON workflow_scheduler_holders(run_id, node_id, iteration, expires_at);

CREATE TABLE workflow_scheduler_waiters (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    priority INTEGER NOT NULL,
    enqueued_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

CREATE INDEX idx_workflow_scheduler_waiters_order
    ON workflow_scheduler_waiters(enqueued_at, priority DESC, run_id, node_id, iteration);

CREATE TABLE workflow_scheduler_admission_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

-- One immutable winning fail-fast trigger per run. The referenced
-- workflow_terminal_intents row remains the sole admission fence and cleanup
-- owner; this row makes concurrent trigger convergence inspectable.
CREATE TABLE workflow_run_policy_decisions (
    run_id TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_json TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_scheduler_resources_immutable
BEFORE UPDATE ON workflow_scheduler_resources BEGIN
    SELECT RAISE(ABORT, 'workflow scheduler resource definitions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_scheduler_resources_reject_delete
BEFORE DELETE ON workflow_scheduler_resources BEGIN
    SELECT RAISE(ABORT, 'workflow scheduler resource definitions are durable configuration');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_run_policy_decisions_immutable
BEFORE UPDATE ON workflow_run_policy_decisions BEGIN
    SELECT RAISE(ABORT, 'workflow run policy decisions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_run_policy_decisions_reject_delete
BEFORE DELETE ON workflow_run_policy_decisions BEGIN
    SELECT RAISE(ABORT, 'workflow run policy decisions are durable history');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0021_workflow_recovery_replay.sql at cfce3b0 ----
-- Restart-durable crash reconciliation and replay provenance. Replay-created
-- runs and invocations remain in the canonical graph-native state tables.

CREATE TABLE workflow_crash_recovery_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL,
    FOREIGN KEY (run_id, node_id, iteration, attempt_number)
        REFERENCES workflow_attempts(run_id, node_id, iteration, attempt_number)
);

CREATE INDEX idx_workflow_crash_recovery_attempt
    ON workflow_crash_recovery_idempotency(run_id, node_id, iteration, attempt_number);

CREATE TABLE workflow_replay_provenance (
    run_id TEXT PRIMARY KEY,
    source_run_id TEXT NOT NULL,
    from_node_id TEXT NOT NULL,
    plan_digest TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    request_json TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id),
    FOREIGN KEY (source_run_id) REFERENCES workflow_runs(id)
);

CREATE INDEX idx_workflow_replay_source
    ON workflow_replay_provenance(source_run_id, created_at, run_id);

-- +goose StatementBegin
CREATE TRIGGER workflow_replay_provenance_reject_update
BEFORE UPDATE ON workflow_replay_provenance
BEGIN
    SELECT RAISE(ABORT, 'workflow replay provenance is immutable');
END;
-- +goose StatementEnd

CREATE TABLE workflow_node_input_bindings (
    idempotency_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL,
    UNIQUE (run_id, node_id, iteration),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_node_input_bindings_reject_update
BEFORE UPDATE ON workflow_node_input_bindings
BEGIN
    SELECT RAISE(ABORT, 'workflow node input binding is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_node_input_bindings_reject_delete
BEFORE DELETE ON workflow_node_input_bindings
BEGIN
    SELECT RAISE(ABORT, 'workflow node input binding is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_crash_recovery_reject_update
BEFORE UPDATE ON workflow_crash_recovery_idempotency
BEGIN
    SELECT RAISE(ABORT, 'workflow crash recovery fact is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_crash_recovery_reject_delete
BEFORE DELETE ON workflow_crash_recovery_idempotency
BEGIN
    SELECT RAISE(ABORT, 'workflow crash recovery fact is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_replay_provenance_reject_delete
BEFORE DELETE ON workflow_replay_provenance
BEGIN
    SELECT RAISE(ABORT, 'workflow replay provenance is immutable');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0022_workflow_memoization.sql at cfce3b0 ----
-- Provenance-safe memoization and run-scoped pinned-output bindings.

ALTER TABLE workflow_node_invocations ADD COLUMN outcome_origin TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_node_invocations ADD COLUMN memo_key_digest TEXT NOT NULL DEFAULT '';

CREATE TABLE workflow_memo_entries (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key TEXT NOT NULL,
    source_run_id TEXT NOT NULL,
    source_node_id TEXT NOT NULL,
    source_iteration TEXT NOT NULL DEFAULT '',
    source_attempt INTEGER NOT NULL CHECK (source_attempt > 0),
    entry_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    UNIQUE (source_run_id, source_node_id, source_iteration, source_attempt),
    FOREIGN KEY (source_run_id, source_node_id, source_iteration, source_attempt)
        REFERENCES workflow_attempts(run_id, node_id, iteration, attempt_number)
);

CREATE INDEX idx_workflow_memo_lookup
    ON workflow_memo_entries(cache_key, created_at DESC, sequence DESC);

-- +goose StatementBegin
CREATE TRIGGER workflow_memo_entries_reject_update
BEFORE UPDATE ON workflow_memo_entries
BEGIN
    SELECT RAISE(ABORT, 'workflow memo entries are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_memo_entries_reject_delete
BEFORE DELETE ON workflow_memo_entries
BEGIN
    SELECT RAISE(ABORT, 'workflow memo entries are append-only');
END;
-- +goose StatementEnd

CREATE TABLE workflow_pin_bindings (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL UNIQUE,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_pin_bindings_reject_update
BEFORE UPDATE ON workflow_pin_bindings
BEGIN
    SELECT RAISE(ABORT, 'workflow pin bindings are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_pin_bindings_reject_delete
BEFORE DELETE ON workflow_pin_bindings
BEGIN
    SELECT RAISE(ABORT, 'workflow pin bindings are immutable');
END;
-- +goose StatementEnd

CREATE TABLE workflow_reuse_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL
);

-- +goose StatementBegin
CREATE TRIGGER workflow_reuse_idempotency_reject_update
BEFORE UPDATE ON workflow_reuse_idempotency
BEGIN
    SELECT RAISE(ABORT, 'workflow reuse outcomes are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_reuse_idempotency_reject_delete
BEFORE DELETE ON workflow_reuse_idempotency
BEGIN
    SELECT RAISE(ABORT, 'workflow reuse outcomes are immutable');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0024_workflow_services.sql at cfce3b0 ----
ALTER TABLE workflow_fanouts
    ADD COLUMN fail_fast INTEGER NOT NULL DEFAULT 0 CHECK (fail_fast IN (0, 1));

CREATE TABLE workflow_services (
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('launching', 'starting', 'ready', 'stopping', 'stopped', 'failed')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    updated_at TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    PRIMARY KEY (run_id, node_id, iteration),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
        ON DELETE RESTRICT
);

CREATE INDEX idx_workflow_services_recovery
    ON workflow_services(status, updated_at, run_id, node_id, iteration);

-- +goose StatementBegin
CREATE TRIGGER workflow_services_reject_delete
BEFORE DELETE ON workflow_services
BEGIN
    SELECT RAISE(ABORT, 'workflow service records are immutable history');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0025_workflow_reactors_updates_failure_hooks.sql at cfce3b0 ----
CREATE INDEX idx_workflow_waits_named_signal
    ON workflow_waits(
        run_id,
        status,
        json_extract(record_json, '$.signal_name'),
        json_extract(record_json, '$.correlation'),
        wait_id
    );

CREATE TABLE workflow_run_updates (
    idempotency_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    signal_name TEXT NOT NULL,
    correlation TEXT NOT NULL,
    wait_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'closed')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    request_json TEXT NOT NULL,
    result_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (wait_id) REFERENCES workflow_waits(wait_id) ON DELETE RESTRICT
);

CREATE INDEX idx_workflow_run_updates_recovery
    ON workflow_run_updates(status, updated_at, idempotency_key);
CREATE INDEX idx_workflow_run_updates_run
    ON workflow_run_updates(run_id, created_at, idempotency_key);

CREATE TABLE workflow_non_durable_runs (
    run_id TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_digest TEXT NOT NULL,
    record_json TEXT NOT NULL,
    completed_at TEXT NOT NULL
);

CREATE INDEX idx_workflow_non_durable_runs_completed
    ON workflow_non_durable_runs(completed_at, run_id);

CREATE TABLE workflow_reactors (
    reactor_id TEXT PRIMARY KEY,
    registration_id TEXT NOT NULL,
    registration_generation INTEGER NOT NULL CHECK (registration_generation > 0),
    correlation TEXT NOT NULL,
    current_generation INTEGER NOT NULL CHECK (current_generation > 0),
    current_run_id TEXT NOT NULL UNIQUE,
    continue_after_events INTEGER NOT NULL CHECK (continue_after_events > 0 AND continue_after_events <= 1000000),
    event_count INTEGER NOT NULL CHECK (event_count >= 0),
    status TEXT NOT NULL CHECK (status IN ('starting', 'waiting', 'rolling', 'failed', 'closed')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    snapshot_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (registration_id, registration_generation, correlation)
);

CREATE TABLE workflow_reactor_generations (
    reactor_id TEXT NOT NULL,
    reactor_generation INTEGER NOT NULL CHECK (reactor_generation > 0),
    run_id TEXT NOT NULL UNIQUE,
    plan_digest TEXT NOT NULL,
    provenance_digest TEXT NOT NULL,
    state_ref_json TEXT,
    event_count INTEGER NOT NULL CHECK (event_count >= 0),
    snapshot_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (reactor_id, reactor_generation),
    FOREIGN KEY (reactor_id) REFERENCES workflow_reactors(reactor_id) ON DELETE RESTRICT
);

CREATE TABLE workflow_reactor_deliveries (
    reactor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    reactor_generation INTEGER NOT NULL CHECK (reactor_generation > 0),
    run_id TEXT NOT NULL,
	starts_generation INTEGER NOT NULL CHECK (starts_generation IN (0, 1)),
	claimed_wait_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('pending', 'applying', 'applied', 'closed')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    request_json TEXT NOT NULL,
    result_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (reactor_id, idempotency_key),
    FOREIGN KEY (reactor_id, reactor_generation)
        REFERENCES workflow_reactor_generations(reactor_id, reactor_generation)
        ON DELETE RESTRICT
);

CREATE INDEX idx_workflow_reactor_deliveries_recovery
    ON workflow_reactor_deliveries(status, updated_at, reactor_id, idempotency_key);

CREATE TABLE workflow_reactor_continuations (
    idempotency_key TEXT PRIMARY KEY,
    reactor_id TEXT NOT NULL,
    from_generation INTEGER NOT NULL CHECK (from_generation > 0),
    to_generation INTEGER NOT NULL CHECK (to_generation > 0),
    from_run_id TEXT NOT NULL UNIQUE,
    to_run_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'started', 'completed')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    request_json TEXT NOT NULL,
    result_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (reactor_id) REFERENCES workflow_reactors(reactor_id) ON DELETE RESTRICT
);

CREATE INDEX idx_workflow_reactor_continuations_recovery
    ON workflow_reactor_continuations(status, updated_at, idempotency_key);

CREATE TABLE workflow_failure_hooks (
    source_run_id TEXT PRIMARY KEY,
    handler_run_id TEXT NOT NULL UNIQUE,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'starting', 'started', 'suppressed', 'failed')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    request_json TEXT NOT NULL,
    result_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_workflow_failure_hooks_recovery
    ON workflow_failure_hooks(status, updated_at, source_run_id);

-- ---- Adapted from Hadron 0028_workflow_plan_snapshots.sql at cfce3b0 ----
-- Exact, locator-sensitive durable plan/source variants. ExecutionPlan.Digest
-- is intentionally relocation-stable, so it cannot identify this material.

CREATE TABLE workflow_plan_snapshots (
    snapshot_digest TEXT PRIMARY KEY,
    plan_digest TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    plan_snapshot_json TEXT NOT NULL,
    source_map_json TEXT NOT NULL,
    source_snapshot_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (plan_digest) REFERENCES workflow_plan_refs(digest)
);

CREATE INDEX idx_workflow_plan_snapshots_plan
    ON workflow_plan_snapshots(plan_digest, snapshot_digest);

CREATE TABLE workflow_run_plan_snapshots (
    run_id TEXT PRIMARY KEY,
    snapshot_digest TEXT NOT NULL,
    plan_digest TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id),
    FOREIGN KEY (snapshot_digest) REFERENCES workflow_plan_snapshots(snapshot_digest),
    FOREIGN KEY (plan_digest) REFERENCES workflow_plan_refs(digest)
);

CREATE INDEX idx_workflow_run_plan_snapshot_ref
    ON workflow_run_plan_snapshots(snapshot_digest, run_id);

-- +goose StatementBegin
CREATE TRIGGER workflow_plan_snapshots_reject_update
BEFORE UPDATE ON workflow_plan_snapshots BEGIN
    SELECT RAISE(ABORT, 'workflow plan snapshots are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_run_plan_snapshots_reject_update
BEFORE UPDATE ON workflow_run_plan_snapshots BEGIN
    SELECT RAISE(ABORT, 'workflow run plan snapshot links are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_run_plan_snapshots_reject_delete
BEFORE DELETE ON workflow_run_plan_snapshots BEGIN
    SELECT RAISE(ABORT, 'workflow run plan snapshot links are durable history');
END;
-- +goose StatementEnd

-- ---- Adapted from Hadron 0029_workflow_compensation.sql at cfce3b0 ----
ALTER TABLE workflow_node_invocations ADD COLUMN phase TEXT NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE TRIGGER workflow_node_invocations_phase_valid_insert
BEFORE INSERT ON workflow_node_invocations
WHEN NEW.phase NOT IN ('', 'compensation')
BEGIN
    SELECT RAISE(ABORT, 'workflow node invocation phase is invalid');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_node_invocations_phase_immutable
BEFORE UPDATE ON workflow_node_invocations
WHEN NEW.phase <> OLD.phase OR NEW.phase NOT IN ('', 'compensation')
BEGIN
    SELECT RAISE(ABORT, 'workflow node invocation phase is immutable');
END;
-- +goose StatementEnd

CREATE TABLE workflow_compensation_ledgers (
    run_id TEXT PRIMARY KEY,
    plan_digest TEXT NOT NULL,
    status TEXT NOT NULL,
    outcome TEXT NOT NULL DEFAULT '',
    generation INTEGER NOT NULL CHECK (generation > 0),
    updated_at TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id),
    FOREIGN KEY (plan_digest) REFERENCES workflow_plan_refs(digest)
);

CREATE INDEX idx_workflow_compensation_recovery
    ON workflow_compensation_ledgers(status, updated_at, run_id);

CREATE TABLE workflow_compensation_entries (
    run_id TEXT NOT NULL,
    entry_id TEXT NOT NULL,
    source_node_id TEXT NOT NULL,
    source_iteration TEXT NOT NULL DEFAULT '',
    source_attempt INTEGER NOT NULL CHECK (source_attempt > 0),
    handler_node_id TEXT NOT NULL,
    handler_iteration TEXT NOT NULL,
    status TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    updated_at TEXT NOT NULL,
    snapshot_json TEXT NOT NULL,
    PRIMARY KEY (run_id, entry_id),
    UNIQUE (run_id, handler_node_id, handler_iteration),
    FOREIGN KEY (run_id) REFERENCES workflow_compensation_ledgers(run_id),
    FOREIGN KEY (run_id, source_node_id, source_iteration, source_attempt)
        REFERENCES workflow_attempts(run_id, node_id, iteration, attempt_number)
);

CREATE INDEX idx_workflow_compensation_entries_status
    ON workflow_compensation_entries(run_id, status, entry_id);

CREATE TABLE workflow_compensation_idempotency (
    idempotency_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    result_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES workflow_compensation_ledgers(run_id)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_ledgers_immutable_identity
BEFORE UPDATE ON workflow_compensation_ledgers
WHEN NEW.run_id <> OLD.run_id OR NEW.plan_digest <> OLD.plan_digest
  OR json_extract(NEW.snapshot_json, '$.created_at') IS NOT json_extract(OLD.snapshot_json, '$.created_at')
  OR (
    NOT (OLD.status = 'collecting' AND NEW.status IN ('frozen', 'terminal'))
    AND (
      json_extract(NEW.snapshot_json, '$.trigger') IS NOT json_extract(OLD.snapshot_json, '$.trigger')
      OR json_extract(NEW.snapshot_json, '$.original_status') IS NOT json_extract(OLD.snapshot_json, '$.original_status')
      OR json_extract(NEW.snapshot_json, '$.original_failure') IS NOT json_extract(OLD.snapshot_json, '$.original_failure')
    )
  )
BEGIN
    SELECT RAISE(ABORT, 'workflow compensation ledger identity is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_entries_immutable_evidence
BEFORE UPDATE ON workflow_compensation_entries
WHEN NEW.run_id <> OLD.run_id OR NEW.entry_id <> OLD.entry_id
  OR NEW.source_node_id <> OLD.source_node_id OR NEW.source_iteration <> OLD.source_iteration
  OR NEW.source_attempt <> OLD.source_attempt OR NEW.handler_node_id <> OLD.handler_node_id
  OR json_extract(NEW.snapshot_json, '$.plan_digest') IS NOT json_extract(OLD.snapshot_json, '$.plan_digest')
  OR json_extract(NEW.snapshot_json, '$.operation') IS NOT json_extract(OLD.snapshot_json, '$.operation')
  OR json_extract(NEW.snapshot_json, '$.evidence_digest') IS NOT json_extract(OLD.snapshot_json, '$.evidence_digest')
  OR json_extract(NEW.snapshot_json, '$.original_inputs') IS NOT json_extract(OLD.snapshot_json, '$.original_inputs')
  OR json_extract(NEW.snapshot_json, '$.original_outputs') IS NOT json_extract(OLD.snapshot_json, '$.original_outputs')
  OR json_extract(NEW.snapshot_json, '$.original_error') IS NOT json_extract(OLD.snapshot_json, '$.original_error')
  OR json_extract(NEW.snapshot_json, '$.receipt') IS NOT json_extract(OLD.snapshot_json, '$.receipt')
  OR json_extract(NEW.snapshot_json, '$.child_run_id') IS NOT json_extract(OLD.snapshot_json, '$.child_run_id')
  OR json_extract(NEW.snapshot_json, '$.created_at') IS NOT json_extract(OLD.snapshot_json, '$.created_at')
  OR (
    NOT (OLD.status = 'eligible' AND NEW.status = 'pending')
    AND json_extract(NEW.snapshot_json, '$.prerequisites') IS NOT json_extract(OLD.snapshot_json, '$.prerequisites')
  )
BEGIN
    SELECT RAISE(ABORT, 'workflow compensation entry evidence is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_ledgers_cycles_append_only
BEFORE UPDATE ON workflow_compensation_ledgers
WHEN OLD.status <> 'collecting' AND (
  coalesce(json_array_length(json_extract(NEW.snapshot_json, '$.cycles')), 0)
    < coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.cycles')), 0)
  OR coalesce(json_array_length(json_extract(NEW.snapshot_json, '$.cycles')), 0)
    > coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.cycles')), 0) + 1
  OR EXISTS (
    SELECT 1
    FROM json_each(json_extract(OLD.snapshot_json, '$.cycles')) AS old_cycle
    LEFT JOIN json_each(json_extract(NEW.snapshot_json, '$.cycles')) AS new_cycle
      ON new_cycle.key = old_cycle.key
    WHERE (
      coalesce(json_array_length(json_extract(NEW.snapshot_json, '$.cycles')), 0)
        > coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.cycles')), 0)
      OR old_cycle.key < coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.cycles')), 0) - 1
    ) AND new_cycle.value IS NOT old_cycle.value
  )
  OR (
    coalesce(json_array_length(json_extract(NEW.snapshot_json, '$.cycles')), 0)
      = coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.cycles')), 0)
    AND (
      json_extract(NEW.snapshot_json, '$.cycles[#-1].number') IS NOT json_extract(OLD.snapshot_json, '$.cycles[#-1].number')
      OR json_extract(NEW.snapshot_json, '$.cycles[#-1].attestation') IS NOT json_extract(OLD.snapshot_json, '$.cycles[#-1].attestation')
      OR json_extract(NEW.snapshot_json, '$.cycles[#-1].started_at') IS NOT json_extract(OLD.snapshot_json, '$.cycles[#-1].started_at')
    )
  )
  OR (
    coalesce(json_array_length(json_extract(NEW.snapshot_json, '$.cycles')), 0)
      = coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.cycles')), 0)
    AND (
      json_extract(OLD.snapshot_json, '$.cycles[#-1].outcome') IS NOT NULL
      AND json_extract(NEW.snapshot_json, '$.cycles[#-1].outcome') IS NOT json_extract(OLD.snapshot_json, '$.cycles[#-1].outcome')
      OR (json_extract(OLD.snapshot_json, '$.cycles[#-1].completed_at') IS NOT NULL
      AND json_extract(OLD.snapshot_json, '$.cycles[#-1].completed_at') <> '0001-01-01T00:00:00Z')
      AND json_extract(NEW.snapshot_json, '$.cycles[#-1].completed_at') IS NOT json_extract(OLD.snapshot_json, '$.cycles[#-1].completed_at')
      OR json_extract(OLD.snapshot_json, '$.cycles[#-1].cancel_reason') IS NOT NULL
      AND json_extract(NEW.snapshot_json, '$.cycles[#-1].cancel_reason') IS NOT json_extract(OLD.snapshot_json, '$.cycles[#-1].cancel_reason')
    )
  )
)
BEGIN
    SELECT RAISE(ABORT, 'workflow compensation cycles are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_entries_history_append_only
BEFORE UPDATE ON workflow_compensation_entries
WHEN coalesce(json_array_length(json_extract(NEW.snapshot_json, '$.history')), 0)
       < coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.history')), 0)
  OR coalesce(json_array_length(json_extract(NEW.snapshot_json, '$.history')), 0)
       > coalesce(json_array_length(json_extract(OLD.snapshot_json, '$.history')), 0) + 1
  OR EXISTS (
    SELECT 1
    FROM json_each(coalesce(json_extract(OLD.snapshot_json, '$.history'), json('[]'))) AS old_history
    LEFT JOIN json_each(coalesce(json_extract(NEW.snapshot_json, '$.history'), json('[]'))) AS new_history
      ON new_history.key = old_history.key
    WHERE new_history.value IS NOT old_history.value
  )
BEGIN
    SELECT RAISE(ABORT, 'workflow compensation entry history is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_ledgers_reject_delete
BEFORE DELETE ON workflow_compensation_ledgers BEGIN
    SELECT RAISE(ABORT, 'workflow compensation ledgers are durable history');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_entries_reject_delete
BEFORE DELETE ON workflow_compensation_entries BEGIN
    SELECT RAISE(ABORT, 'workflow compensation entries are durable history');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_idempotency_reject_update
BEFORE UPDATE ON workflow_compensation_idempotency BEGIN
    SELECT RAISE(ABORT, 'workflow compensation idempotency is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_compensation_idempotency_reject_delete
BEFORE DELETE ON workflow_compensation_idempotency BEGIN
    SELECT RAISE(ABORT, 'workflow compensation idempotency is durable history');
END;
-- +goose StatementEnd


-- Immutable metadata required to project public runtime snapshots onto
-- Nanite's existing workflow_run_steps schema without consulting mutable
-- definition files during recovery.
CREATE TABLE workflow_plan_node_projections (
    plan_digest TEXT NOT NULL,
    node_id TEXT NOT NULL,
    product_step_id TEXT NOT NULL,
    product_kind TEXT NOT NULL
        CHECK(product_kind IN ('llm','tool','gate','flex','loop')),
    wait_class TEXT NOT NULL DEFAULT ''
        CHECK(wait_class IN ('','gate','flex','loop')),
    PRIMARY KEY (plan_digest, node_id),
    FOREIGN KEY (plan_digest) REFERENCES workflow_plan_refs(digest)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_plan_node_projections_reject_update
BEFORE UPDATE ON workflow_plan_node_projections
BEGIN
    SELECT RAISE(ABORT, 'workflow plan node projections are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_plan_node_projections_reject_delete
BEFORE DELETE ON workflow_plan_node_projections
BEGIN
    SELECT RAISE(ABORT, 'workflow plan node projections are durable plan history');
END;
-- +goose StatementEnd


-- +goose Down
-- Downgrade is destructive to shared-runtime history and is intended only
-- before the pilot creates shared-engine runs.

DROP TABLE IF EXISTS workflow_compensation_idempotency;
DROP TABLE IF EXISTS workflow_compensation_entries;
DROP TABLE IF EXISTS workflow_compensation_ledgers;
DROP TABLE IF EXISTS workflow_run_plan_snapshots;
DROP TABLE IF EXISTS workflow_plan_node_projections;
DROP TABLE IF EXISTS workflow_plan_snapshots;
DROP TABLE IF EXISTS workflow_failure_hooks;
DROP TABLE IF EXISTS workflow_reactor_continuations;
DROP TABLE IF EXISTS workflow_reactor_deliveries;
DROP TABLE IF EXISTS workflow_reactor_generations;
DROP TABLE IF EXISTS workflow_reactors;
DROP TABLE IF EXISTS workflow_non_durable_runs;
DROP TABLE IF EXISTS workflow_run_updates;
DROP TABLE IF EXISTS workflow_services;
DROP TABLE IF EXISTS workflow_reuse_idempotency;
DROP TABLE IF EXISTS workflow_pin_bindings;
DROP TABLE IF EXISTS workflow_memo_entries;
DROP TABLE IF EXISTS workflow_node_input_bindings;
DROP TABLE IF EXISTS workflow_replay_provenance;
DROP TABLE IF EXISTS workflow_crash_recovery_idempotency;
DROP TABLE IF EXISTS workflow_run_policy_decisions;
DROP TABLE IF EXISTS workflow_scheduler_admission_idempotency;
DROP TABLE IF EXISTS workflow_scheduler_waiters;
DROP TABLE IF EXISTS workflow_scheduler_holders;
DROP TABLE IF EXISTS workflow_scheduler_resources;
DROP TABLE IF EXISTS workflow_control_cancellation_trees;
DROP TABLE IF EXISTS workflow_terminal_intents;
DROP TABLE IF EXISTS workflow_control_decisions;
DROP TABLE IF EXISTS workflow_run_cancellation_idempotency;
DROP TABLE IF EXISTS workflow_cancellation_intents;
DROP TABLE IF EXISTS workflow_child_runs;
DROP TABLE IF EXISTS workflow_fanout_items;
DROP TABLE IF EXISTS workflow_fanouts;
DROP TABLE IF EXISTS workflow_retry_activation_idempotency;
DROP TABLE IF EXISTS workflow_retry_activations;
DROP TABLE IF EXISTS workflow_external_operations;
DROP TABLE IF EXISTS workflow_wait_attempt_bindings;
DROP TABLE IF EXISTS workflow_wait_timeout_idempotency;
DROP TABLE IF EXISTS workflow_wait_resume_results;
DROP TABLE IF EXISTS workflow_wait_suspend_idempotency;
DROP TABLE IF EXISTS workflow_external_activations;
DROP TABLE IF EXISTS workflow_pinned_values;
DROP TABLE IF EXISTS workflow_cache_entries;
DROP TABLE IF EXISTS workflow_events;
DROP TABLE IF EXISTS workflow_event_sequences;
DROP TABLE IF EXISTS workflow_value_sets;
DROP TABLE IF EXISTS workflow_wait_resume_idempotency;
DROP TABLE IF EXISTS workflow_waits;
DROP TABLE IF EXISTS workflow_attempts;
DROP TABLE IF EXISTS workflow_claim_idempotency;
DROP TABLE IF EXISTS workflow_node_leases;
DROP TABLE IF EXISTS workflow_node_invocations;
DROP TABLE IF EXISTS workflow_run_start_idempotency;
DROP INDEX IF EXISTS idx_workflow_runs_runtime_recovery;
ALTER TABLE workflow_runs DROP COLUMN runtime_failure_json;
ALTER TABLE workflow_runs DROP COLUMN runtime_generation;
ALTER TABLE workflow_runs DROP COLUMN outputs_ref_json;
ALTER TABLE workflow_runs DROP COLUMN inputs_ref_json;
ALTER TABLE workflow_runs DROP COLUMN runtime_status;
ALTER TABLE workflow_runs DROP COLUMN plan_digest;
ALTER TABLE workflow_runs DROP COLUMN engine_contract_version;
ALTER TABLE workflow_runs DROP COLUMN engine_kind;
DROP TABLE IF EXISTS workflow_plan_refs;
