package store

import (
	"context"
	"reflect"
	"testing"
)

// Private historical fixtures do not enroll an actor or restore legacy APIs.
func retiredBehaviorReflexFixture(t *testing.T, s *Store, row AgentReflex) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(), `INSERT INTO agent_reflexes(id,agent_id,class_tag,name,trigger_kind,trigger_spec,action_kind,action_spec,status,priority,fired_count,last_fired_at,created_at,created_by,opt_out_allowed,provenance_tier,workflow_run_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, row.ID, nullIfEmpty(row.AgentID), nullIfEmpty(row.ClassTag), row.Name, "event", `{"name":"historical-probe"}`, "inject_reminder", `{"body":"operator edited historical reminder"}`, "paused", 77, 9, "2026-10-01", "2026-09-01", row.CreatedBy, true, row.ProvenanceTier, nullIfEmpty(row.WorkflowRunID)); err != nil {
		t.Fatal(err)
	}
}

func TestImmutablePluginReflexAdoptionPreservesHistoricalOwnershipAndTombstones(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	const agentID = "historical-adoption"
	retiredBehaviorHistoricalProfile(t, s, agentID)
	retiredBehaviorReflexFixture(t, s, AgentReflex{ID: "retained-adoption", AgentID: agentID, Name: "check_before_answer", CreatedBy: "system", ProvenanceTier: "system"})
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_reflex_opt_outs(agent_id,reflex_id,created_at) VALUES(?,'retained-adoption','retained-opt-out')`, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO plugin_reflex_seed_bindings(plugin_id,seed_id,agent_id,reflex_id) VALUES('nanite.loom','deleted',?,NULL)`, agentID); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_reflexes ORDER BY id`, `SELECT * FROM agent_reflex_opt_outs ORDER BY agent_id,reflex_id`, `SELECT * FROM plugin_reflex_seed_bindings ORDER BY plugin_id,seed_id,agent_id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredBehaviorSnapshot(t, s, q)
	}
	seed := ExistingPluginReflexSeed{SeedID: "check-before-answer", AgentID: agentID, LegacyName: "check_before_answer"}
	for _, seeds := range [][]ExistingPluginReflexSeed{
		{seed}, {seed, {SeedID: "bad", AgentID: "missing", LegacyName: "missing"}},
		{{SeedID: "deleted", AgentID: agentID, LegacyName: seed.LegacyName}},
		{{SeedID: "absent", AgentID: agentID, LegacyName: "absent"}}, nil,
	} {
		rows, err := s.BindExistingPluginReflexSeeds(ctx, "nanite.loom", seeds)
		requireRetiredBehavior(t, err)
		if len(rows) != 0 {
			t.Fatal("refused adoption returned runtime definitions")
		}
	}
	for i, q := range queries {
		if after := retiredBehaviorSnapshot(t, s, q); !reflect.DeepEqual(before[i], after) {
			t.Fatalf("refused adoption changed history for %s: before=%v after=%v", q, before[i], after)
		}
	}
}

func TestImmutablePluginReflexAdoptionCannotClaimOperatorOrAmbiguousHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	const agentID = "historical-ambiguous-adoption"
	retiredBehaviorHistoricalProfile(t, s, agentID)
	retiredBehaviorReflexFixture(t, s, AgentReflex{ID: "operator-owned", AgentID: agentID, Name: "capture", CreatedBy: "user", ProvenanceTier: "operator"})
	const query = `SELECT * FROM agent_reflexes ORDER BY id`
	seed := ExistingPluginReflexSeed{SeedID: "capture", AgentID: agentID, LegacyName: "capture"}
	for _, ambiguous := range []bool{false, true} {
		if ambiguous {
			retiredBehaviorReflexFixture(t, s, AgentReflex{ID: "same-name-system", AgentID: agentID, Name: "capture", CreatedBy: "system", ProvenanceTier: "system"})
		}
		before := retiredBehaviorSnapshot(t, s, query)
		rows, err := s.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{seed})
		requireRetiredBehavior(t, err)
		if len(rows) != 0 {
			t.Fatal("historical ownership was claimed")
		}
		if after := retiredBehaviorSnapshot(t, s, query); !reflect.DeepEqual(before, after) {
			t.Fatal("historical ownership or data changed")
		}
		if bindings := retiredBehaviorSnapshot(t, s, `SELECT * FROM plugin_reflex_seed_bindings`); len(bindings) != 0 {
			t.Fatal("refused adoption left bindings", bindings)
		}
	}
}
