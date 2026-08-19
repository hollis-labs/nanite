package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate115AddsOptOutColumnAndTable is the regression test for
// TASKS/phase-1/07-add-reflex-opt-out-field.md: agent_reflexes gains a
// permissive-by-default opt_out_allowed column, and a new
// agent_reflex_opt_outs join table exists with ON DELETE CASCADE FKs to
// both agent_profiles and agent_reflexes.
func TestMigrate115AddsOptOutColumnAndTable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// InsertAgentReflex always binds OptOutAllowed explicitly (so a Go
	// caller's zero-value bool is never accidentally silently upgraded to
	// true) — every real call site in this codebase sets it deliberately
	// (seeds.go's Required-derived value, loom_pilot_seeds.go's explicit
	// true, and internal/api/reflexes.go's *bool-with-default-true
	// request field). The column's own DEFAULT TRUE is a backstop for
	// non-Go-layer inserts: pre-migration rows backfilled by 115's own Up,
	// and ApprovePendingReflex's raw SQL INSERT, which omits the column on
	// purpose. Verify that backstop directly via a raw SQL insert that
	// mirrors ApprovePendingReflex's own column list (no opt_out_allowed).
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_reflexes
		    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
		     action_kind, action_spec, status, priority, fired_count,
		     last_fired_at, created_at, created_by)
		 VALUES ('rfx-migration-115-probe', NULL, 'process', 'migration-115-probe',
		         'event', '{"name":"probe"}', 'inject_reminder', '{"body":"probe"}',
		         'active', 0, 0, NULL, datetime('now'), 'test')`,
	); err != nil {
		t.Fatalf("raw insert omitting opt_out_allowed: %v", err)
	}
	id := "rfx-migration-115-probe"
	var optOutAllowed bool
	if err := s.DB.QueryRowContext(ctx,
		`SELECT opt_out_allowed FROM agent_reflexes WHERE id = ?`, id,
	).Scan(&optOutAllowed); err != nil {
		t.Fatalf("read opt_out_allowed: %v", err)
	}
	if !optOutAllowed {
		t.Errorf("row inserted with opt_out_allowed omitted got false, want true (column DEFAULT)")
	}

	// The opt-out table exists and enforces its FKs / PK.
	agent := &AgentProfile{Name: "Opt-Out Probe", Slug: "opt-out-probe", SystemPrompt: "x", Class: "process"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := s.SetAgentReflexOptOut(ctx, agent.ID, id); err != nil {
		t.Fatalf("SetAgentReflexOptOut: %v", err)
	}
	// Idempotent re-insert.
	if err := s.SetAgentReflexOptOut(ctx, agent.ID, id); err != nil {
		t.Fatalf("SetAgentReflexOptOut (repeat): %v", err)
	}
	outs, err := s.ListAgentReflexOptOuts(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentReflexOptOuts: %v", err)
	}
	if len(outs) != 1 || outs[0] != id {
		t.Fatalf("ListAgentReflexOptOuts = %v, want [%s]", outs, id)
	}

	// FK insert against a nonexistent reflex_id must fail under
	// foreign_keys=ON.
	if err := s.SetAgentReflexOptOut(ctx, agent.ID, "rfx-does-not-exist"); err == nil {
		t.Error("SetAgentReflexOptOut against a nonexistent reflex_id should fail the FK constraint")
	}

	// Deleting the reflex cascades the opt-out row away (no manual
	// cleanup needed — see DeleteAgentReflex's doc comment).
	if err := s.DeleteAgentReflex(ctx, id); err != nil {
		t.Fatalf("DeleteAgentReflex: %v", err)
	}
	outs, err = s.ListAgentReflexOptOuts(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentReflexOptOuts after reflex delete: %v", err)
	}
	if len(outs) != 0 {
		t.Errorf("ListAgentReflexOptOuts after reflex delete = %v, want none (cascade)", outs)
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after 115 already applied: %v", err)
	}
}

// TestMigrate115AgentDeleteCascadesOptOuts verifies the other cascade
// direction: deleting the opting-out agent removes its opt-out rows too
// (agent_reflex_opt_outs.agent_id ON DELETE CASCADE), exercised against a
// class-bound reflex the agent has opted out of (the real-world shape:
// opting out of a global/class-bound reflex, not an agent-owned one).
func TestMigrate115AgentDeleteCascadesOptOuts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	classReflexID, err := s.InsertAgentReflex(ctx, AgentReflex{
		ClassTag:      "process",
		Name:          "class-bound-probe",
		TriggerKind:   ReflexTriggerEvent,
		TriggerSpec:   `{"name":"probe"}`,
		ActionKind:    ReflexActionInjectReminder,
		ActionSpec:    `{"body":"probe"}`,
		OptOutAllowed: true,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex (class-bound): %v", err)
	}

	agent := &AgentProfile{Name: "Opt-Out Agent Delete Probe", Slug: "opt-out-agent-delete-probe", SystemPrompt: "x", Class: "process"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := s.SetAgentReflexOptOut(ctx, agent.ID, classReflexID); err != nil {
		t.Fatalf("SetAgentReflexOptOut: %v", err)
	}

	if err := s.DeleteAgent(agent.Slug); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_reflex_opt_outs WHERE reflex_id = ?`, classReflexID,
	).Scan(&n); err != nil {
		t.Fatalf("count opt-outs after agent delete: %v", err)
	}
	if n != 0 {
		t.Errorf("agent_reflex_opt_outs rows after DeleteAgent = %d, want 0 (cascade)", n)
	}
	// The class-bound reflex itself must survive — DeleteAgent only owns
	// the deleted agent's own children, not global reflexes it merely
	// opted out of.
	if _, err := s.GetAgentReflex(ctx, classReflexID); err != nil {
		t.Errorf("class-bound reflex should survive the opting-out agent's deletion: %v", err)
	}
}

// TestMigrate115DownDropsOptOutColumnAndTable is the tested Down half of
// migration 115.
func TestMigrate115DownDropsOptOutColumnAndTable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 114); err != nil {
		t.Fatalf("goose DownTo 114 (reverse migration 115): %v", err)
	}

	// opt_out_allowed no longer exists.
	var probe bool
	err = s.DB.QueryRowContext(ctx, `SELECT opt_out_allowed FROM agent_reflexes LIMIT 1`).Scan(&probe)
	if err == nil {
		t.Error("opt_out_allowed column should not exist after Down")
	}

	// agent_reflex_opt_outs no longer exists.
	if _, err := s.DB.ExecContext(ctx, `SELECT 1 FROM agent_reflex_opt_outs LIMIT 1`); err == nil {
		t.Error("agent_reflex_opt_outs table should not exist after Down")
	}

	// Insert must still work against the pre-115 shape (no opt_out_allowed
	// column to satisfy). Uses a raw INSERT rather than InsertAgentReflex —
	// InsertAgentReflex is current Go code, which always binds
	// opt_out_allowed and therefore assumes the current (post-115) schema;
	// a real rollback pairs the older schema with the older Go binary, so
	// exercising that combination here would test a mismatch that never
	// actually occurs in production.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_reflexes
		    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
		     action_kind, action_spec, status, priority, fired_count,
		     last_fired_at, created_at, created_by)
		 VALUES ('rfx-post-down-probe', NULL, 'process', 'post-down-probe',
		         'event', '{"name":"probe"}', 'inject_reminder', '{"body":"probe"}',
		         'active', 0, 0, NULL, datetime('now'), 'test')`,
	); err != nil {
		t.Fatalf("raw insert after Down: %v", err)
	}

	// Back up to the top so the store is left fully migrated again,
	// mirroring the shape of a real down-then-up-again cycle.
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up after DownTo 114: %v", err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT opt_out_allowed FROM agent_reflexes LIMIT 1`).Scan(&probe); err != nil {
		t.Errorf("opt_out_allowed column should exist again after replaying Up: %v", err)
	}
}
