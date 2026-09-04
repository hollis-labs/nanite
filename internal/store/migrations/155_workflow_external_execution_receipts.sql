-- +goose Up
-- One keyed execution receipt accompanies the shared runtime's authoritative
-- workflow_external_operations row. The receipt distinguishes a prepared
-- outbox intent from a subprocess call that may have escaped before its result
-- was committed. A surviving pending call is made ambiguous and must never be
-- invoked again automatically.
CREATE TABLE workflow_external_execution_receipts (
    idempotency_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    iteration TEXT NOT NULL DEFAULT '',
    request_digest TEXT NOT NULL,
    request_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (
        state IN ('prepared', 'pending', 'completed', 'ambiguous')
    ),
    output TEXT,
    is_error INTEGER CHECK (is_error IN (0, 1)),
    ambiguity_reason TEXT NOT NULL DEFAULT '',
    resolution_key TEXT NOT NULL DEFAULT '',
    resolution_action TEXT NOT NULL DEFAULT '',
    resolution_actor TEXT NOT NULL DEFAULT '',
    resolution_reason TEXT NOT NULL DEFAULT '',
    resolved_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (run_id, node_id, iteration),
    FOREIGN KEY (run_id, node_id, iteration)
        REFERENCES workflow_node_invocations(run_id, node_id, iteration)
);

CREATE INDEX idx_workflow_external_execution_receipts_recovery
    ON workflow_external_execution_receipts(state, updated_at, run_id, node_id, iteration);

-- Request identity and workflow ownership are immutable. State and outcome
-- advance only through generation-serialized host transactions.
-- +goose StatementBegin
CREATE TRIGGER workflow_external_execution_receipts_immutable_binding
BEFORE UPDATE ON workflow_external_execution_receipts
WHEN OLD.idempotency_key <> NEW.idempotency_key
  OR OLD.run_id <> NEW.run_id
  OR OLD.node_id <> NEW.node_id
  OR OLD.iteration <> NEW.iteration
  OR OLD.request_digest <> NEW.request_digest
  OR OLD.request_json <> NEW.request_json
BEGIN
    SELECT RAISE(ABORT, 'workflow external execution receipt binding is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_external_execution_receipts_reject_delete
BEFORE DELETE ON workflow_external_execution_receipts
BEGIN
    SELECT RAISE(ABORT, 'workflow external execution receipts are durable history');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS workflow_external_execution_receipts_reject_delete;
DROP TRIGGER IF EXISTS workflow_external_execution_receipts_immutable_binding;
DROP TABLE IF EXISTS workflow_external_execution_receipts;
