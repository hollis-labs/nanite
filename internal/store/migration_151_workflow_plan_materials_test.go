package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigration151AddsImmutableExactPlanMaterial(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("fs.Sub migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("goose.NewProvider: %v", err)
	}
	if _, err := provider.DownTo(ctx, 150); err != nil {
		t.Fatalf("DownTo(150): %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up migration 151: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_plan_refs(digest, plan_id, version, schema_version)
VALUES ('sha256:plan-151', 'plan-151', 'v1', '1')`); err != nil {
		t.Fatalf("insert plan ref: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_plan_materials(
    plan_digest, plan_json, visibility_json, source_locator, source_format,
    source_digest, source_content, product_definition_name,
    stepkind_catalog_json, stepkind_catalog_digest, verifier_catalog_json,
    verifier_catalog_digest, host_contract_json, host_contract_digest, created_at
) VALUES (?, '{}', '{}', 'fixture.workflow.yaml', 'workflow', ?, ?, 'fixture workflow',
          '[]', ?, '[]', ?, '[]', ?, ?)`,
		"sha256:plan-151", "sha256:source-151", []byte("workflow: fixture"),
		"sha256:stepkind-catalog-151", "sha256:verifier-catalog-151",
		"sha256:host-contract-151", "2026-09-04T12:00:00Z",
	); err != nil {
		t.Fatalf("insert plan material: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE workflow_plan_materials SET source_locator='moved' WHERE plan_digest='sha256:plan-151'`); err == nil {
		t.Fatal("workflow plan material update unexpectedly succeeded")
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM workflow_plan_materials WHERE plan_digest='sha256:plan-151'`); err == nil {
		t.Fatal("workflow plan material delete unexpectedly succeeded")
	}
	if _, err := provider.DownTo(ctx, 150); err != nil {
		t.Fatalf("DownTo(150): %v", err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='workflow_plan_materials'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("workflow_plan_materials table count after Down = %d", count)
	}
}
