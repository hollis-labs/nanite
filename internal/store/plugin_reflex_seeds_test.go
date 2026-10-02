package store

import (
	"context"
	"testing"
)

func TestPluginReflexBindingPreservesEditsHistoryDeletionAndRollback(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	agent := &AgentProfile{ID: "plugin-seed-agent", Slug: "plugin-seed-agent", Name: "Seed target", SystemPrompt: "test"}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	seed := PluginReflexSeed{SeedID: "remind", Definition: AgentReflex{ID: "plugin-seed-reflex", AgentID: agent.ID, Name: "bookmarks:remind", TriggerKind: ReflexTriggerPredicate, TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`, ActionKind: ReflexActionInjectReminder, ActionSpec: `{"body":"initial"}`, CreatedBy: "plugin:bookmarks", ProvenanceTier: "plugin", OptOutAllowed: true}}
	rows, err := st.BindPluginReflexSeeds(ctx, "bookmarks", []PluginReflexSeed{seed})
	if err != nil || len(rows) != 1 {
		t.Fatalf("bind: %v %+v", err, rows)
	}
	candidates, err := st.ListAgentReflexesForAgent(ctx, agent.ID, "")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("unregistered plugin fired: %v %+v", err, candidates)
	}
	catalog, catalogErr := st.ListAllAgentReflexes(ctx, agent.ID)
	if catalogErr != nil || len(catalog) != 1 {
		t.Fatalf("inactive definition disappeared from editor: %v %+v", catalogErr, catalog)
	}
	st.SetPluginReflexGate(func(r AgentReflex) bool { return r.ID == seed.Definition.ID })
	candidates, err = st.ListAgentReflexesForAgent(ctx, agent.ID, "")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("registered plugin unavailable: %v %+v", err, candidates)
	}
	if err = st.SetAgentReflexOptOut(ctx, agent.ID, seed.Definition.ID); err != nil {
		t.Fatal(err)
	}
	candidates, err = st.ListAgentReflexesForAgent(ctx, agent.ID, "")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("explicit plugin target ignored opt-out: %v %+v", err, candidates)
	}
	if err = st.ClearAgentReflexOptOut(ctx, agent.ID, seed.Definition.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, `UPDATE agent_reflexes SET name='edited',action_spec='{"body":"edited"}',priority=77,fired_count=9,last_fired_at='2026-10-01',status='paused' WHERE id=?`, seed.Definition.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = st.BindPluginReflexSeeds(ctx, "bookmarks", []PluginReflexSeed{seed})
	if err != nil || len(rows) != 1 || rows[0].Name != "edited" || rows[0].ActionSpec != `{"body":"edited"}` || rows[0].Priority != 77 || rows[0].FiredCount != 9 || rows[0].LastFiredAt != "2026-10-01" || rows[0].Status != ReflexStatusPaused {
		t.Fatalf("reload overwrote edits/history: %v %+v", err, rows)
	}
	st.SetPluginReflexGate(nil)
	if _, err = st.DB.ExecContext(ctx, `DELETE FROM agent_reflexes WHERE id=?`, seed.Definition.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = st.BindPluginReflexSeeds(ctx, "bookmarks", []PluginReflexSeed{seed})
	if err != nil || len(rows) != 0 {
		t.Fatalf("reload recreated deleted default: %v %+v", err, rows)
	}
	fresh := seed
	fresh.SeedID = "fresh"
	fresh.Definition.ID = "fresh-id"
	bad := seed
	bad.SeedID = "bad"
	bad.Definition.ID = "bad-id"
	bad.Definition.AgentID = "missing-agent"
	if _, err = st.BindPluginReflexSeeds(ctx, "bookmarks", []PluginReflexSeed{fresh, bad}); err == nil {
		t.Fatal("accepted missing target")
	}
	var count int
	if err = st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_reflexes WHERE id='fresh-id'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial defaults survived rollback: %v count=%d", err, count)
	}
}
