package store

import (
	"context"
	"reflect"
	"testing"
)

func TestImmutablePluginReflexSeedsRefuseReloadAndPreserveEditedHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	const agentID = "historical-plugin-seeds"
	retiredBehaviorHistoricalProfile(t, s, agentID)
	retiredBehaviorReflexFixture(t, s, AgentReflex{ID: "edited-plugin-reflex", AgentID: agentID, Name: "edited", CreatedBy: "plugin:bookmarks", ProvenanceTier: "plugin"})
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO plugin_reflex_seed_bindings(plugin_id,seed_id,agent_id,reflex_id) VALUES('bookmarks','remind',?,'edited-plugin-reflex'),('bookmarks','deleted',?,NULL)`, agentID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_reflex_opt_outs(agent_id,reflex_id) VALUES(?,'edited-plugin-reflex')`, agentID); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_reflexes ORDER BY id`, `SELECT * FROM plugin_reflex_seed_bindings ORDER BY plugin_id,seed_id,agent_id`, `SELECT * FROM agent_reflex_opt_outs ORDER BY agent_id,reflex_id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredBehaviorSnapshot(t, s, q)
	}
	seed := PluginReflexSeed{SeedID: "remind", Definition: AgentReflex{ID: "edited-plugin-reflex", AgentID: agentID, Name: "replacement", TriggerKind: ReflexTriggerEvent, TriggerSpec: `{"name":"probe"}`, ActionKind: ReflexActionInjectReminder, ActionSpec: `{"body":"replacement"}`, CreatedBy: "plugin:bookmarks", ProvenanceTier: "plugin", OptOutAllowed: true}}
	fresh := seed
	fresh.SeedID = "fresh"
	fresh.Definition.ID = "fresh-id"
	deleted := seed
	deleted.SeedID = "deleted"
	deleted.Definition.ID = "deleted-id"
	bad := seed
	bad.SeedID = "bad"
	bad.Definition.ID = "bad-id"
	bad.Definition.AgentID = "missing"
	gateCalls := 0
	s.SetPluginReflexGate(func(AgentReflex) bool { gateCalls++; return true })
	defer s.SetPluginReflexGate(nil)
	for _, seeds := range [][]PluginReflexSeed{{seed}, {fresh}, {deleted}, {fresh, bad}, nil} {
		rows, err := s.BindPluginReflexSeeds(ctx, "bookmarks", seeds)
		requireRetiredBehavior(t, err)
		if len(rows) != 0 {
			t.Fatal("refused reload returned legacy executable definitions")
		}
	}
	candidates, err := s.ListAgentReflexesForAgent(ctx, agentID, "")
	requireRetiredBehavior(t, err)
	if len(candidates) != 0 {
		t.Fatal("registered plugin restored legacy behavior")
	}
	catalog, err := s.ListAllAgentReflexes(ctx, agentID)
	requireRetiredBehavior(t, err)
	if len(catalog) != 0 {
		t.Fatal("historical definitions exposed through mutable catalog")
	}
	if gateCalls != 0 {
		t.Fatal("legacy refusal evaluated executable plugin gate", gateCalls)
	}
	for i, q := range queries {
		if after := retiredBehaviorSnapshot(t, s, q); !reflect.DeepEqual(before[i], after) {
			t.Fatalf("reload changed retained history for %s: before=%v after=%v", q, before[i], after)
		}
	}
}
