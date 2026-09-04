-- +goose Up
-- 154_workflow_run_definition_revision.sql
--
-- Bind each shared-engine run to the exact immutable definition revision
-- whose compiled plan it executes. Historical/conformance rows may remain
-- NULL because their authored source cannot be reconstructed truthfully.

ALTER TABLE workflow_runs
    ADD COLUMN definition_revision_id TEXT
        REFERENCES workflow_definition_revisions(revision_id);

CREATE INDEX idx_workflow_runs_definition_revision
    ON workflow_runs(definition_revision_id)
    WHERE definition_revision_id IS NOT NULL;

-- There must be exactly one catalog revision for the tuple available to the
-- host's plan-first atomic run insert. This prevents a mutable head lookup or
-- arbitrary tie-breaking from selecting run identity.
CREATE UNIQUE INDEX idx_workflow_definition_revisions_exact_plan
    ON workflow_definition_revisions(
        definition_name,
        compiled_plan_digest,
        engine_kind,
        engine_contract_version
    );

-- +goose Down

DROP INDEX IF EXISTS idx_workflow_definition_revisions_exact_plan;
DROP INDEX IF EXISTS idx_workflow_runs_definition_revision;
ALTER TABLE workflow_runs DROP COLUMN definition_revision_id;
