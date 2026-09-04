-- +goose Up
-- 151_workflow_plan_materials.sql
-- Immutable, restart-safe material for every Hadron execution plan accepted by
-- the Nanite host. The public runtime stores only PlanRef on workflow_runs;
-- recovery must never recompile from a movable definition or mutable registry.

CREATE TABLE workflow_plan_materials (
    plan_digest TEXT PRIMARY KEY,
    plan_json TEXT NOT NULL,
    visibility_json TEXT NOT NULL,
    source_locator TEXT NOT NULL,
    source_format TEXT NOT NULL CHECK (source_format IN ('workflow', 'sdk', 'ui', 'agent')),
    source_digest TEXT NOT NULL,
    source_content BLOB NOT NULL,
    product_definition_name TEXT NOT NULL,
    stepkind_catalog_json TEXT NOT NULL,
    stepkind_catalog_digest TEXT NOT NULL,
    verifier_catalog_json TEXT NOT NULL,
    verifier_catalog_digest TEXT NOT NULL,
    host_contract_json TEXT NOT NULL,
    host_contract_digest TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (plan_digest) REFERENCES workflow_plan_refs(digest)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_plan_materials_no_update
BEFORE UPDATE ON workflow_plan_materials
BEGIN
    SELECT RAISE(ABORT, 'workflow plan material is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_plan_materials_no_delete
BEFORE DELETE ON workflow_plan_materials
BEGIN
    SELECT RAISE(ABORT, 'workflow plan material is immutable');
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER IF EXISTS workflow_plan_materials_no_delete;
DROP TRIGGER IF EXISTS workflow_plan_materials_no_update;
DROP TABLE IF EXISTS workflow_plan_materials;
