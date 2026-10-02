package store

import (
	"context"
	"testing"
)

func TestExistingPluginSeedAdoptionPreservesDataDeletionAndRollback(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	agent := &AgentProfile{Slug: "loom-weaver", Name: "Weaver", SystemPrompt: "test"}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	originalID, err := st.InsertAgentReflex(ctx, AgentReflex{AgentID: agent.ID, Name: "check_before_answer", TriggerKind: ReflexTriggerPredicate, TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`, ActionKind: ReflexActionInjectReminder, ActionSpec: `{"body":"operator edited reminder","urgency":"info"}`, Status: ReflexStatusPaused, Priority: 77, FiredCount: 9, LastFiredAt: "2026-10-01", CreatedBy: "system", OptOutAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	original, err := st.GetAgentReflex(ctx, originalID)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetAgentReflexOptOut(ctx, agent.ID, original.ID); err != nil {
		t.Fatal(err)
	}
	seed := ExistingPluginReflexSeed{SeedID: "check-before-answer", AgentID: agent.ID, LegacyName: original.Name}
	bad := ExistingPluginReflexSeed{SeedID: "bad", AgentID: "missing-agent", LegacyName: "missing"}
	if _, err = st.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{seed, bad}); err == nil {
		t.Fatal("partial handoff accepted")
	}
	unchanged, err := st.GetAgentReflex(ctx, original.ID)
	if err != nil || unchanged.CreatedBy != "system" || unchanged.ProvenanceTier != "system" {
		t.Fatal("rollback changed source", unchanged, err)
	}
	var count int
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_reflex_seed_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback retained bindings", count, err)
	}
	rows, err := st.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{seed})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	got := rows[0]
	if got.ID != original.ID || got.Name != original.Name || got.TriggerSpec != original.TriggerSpec || got.ActionSpec != original.ActionSpec || got.Status != original.Status || got.Priority != original.Priority || got.FiredCount != original.FiredCount || got.LastFiredAt != original.LastFiredAt || got.CreatedAt != original.CreatedAt || got.CreatedBy != "plugin:nanite.loom" || got.ProvenanceTier != "plugin" {
		t.Fatal("handoff changed data", got)
	}
	optouts, err := st.ListAgentReflexOptOuts(ctx, agent.ID)
	if err != nil || len(optouts) != 1 || optouts[0] != original.ID {
		t.Fatal("handoff lost opt-out", optouts, err)
	}
	if err = st.DeleteAgentReflex(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = st.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{seed})
	if err != nil || len(rows) != 0 {
		t.Fatal("recreated deleted transferred definition", rows, err)
	}
	missing := ExistingPluginReflexSeed{SeedID: "curator-capture-on-discovery", AgentID: agent.ID, LegacyName: "capture_on_discovery"}
	rows, err = st.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{missing})
	if err != nil || len(rows) != 0 {
		t.Fatal("recreated absent source", rows, err)
	}
	if _, err = st.InsertAgentReflex(ctx, AgentReflex{AgentID: agent.ID, Name: missing.LegacyName, TriggerKind: ReflexTriggerPredicate, TriggerSpec: original.TriggerSpec, ActionKind: ReflexActionInjectReminder, ActionSpec: original.ActionSpec, CreatedBy: "system", OptOutAllowed: true}); err != nil {
		t.Fatal(err)
	}
	rows, err = st.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{missing})
	if err != nil || len(rows) != 0 {
		t.Fatal("tombstone overwritten by later source", rows, err)
	}
}
func TestExistingPluginSeedAdoptionRefusesAmbiguousAndOperatorSources(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	agent := &AgentProfile{Slug: "loom-curator", Name: "Curator", SystemPrompt: "test"}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	row := AgentReflex{AgentID: agent.ID, Name: "capture_on_discovery", TriggerKind: ReflexTriggerPredicate, TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`, ActionKind: ReflexActionInjectReminder, ActionSpec: `{"body":"custom"}`, CreatedBy: "user", OptOutAllowed: true}
	originalID, err := st.InsertAgentReflex(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	seed := ExistingPluginReflexSeed{SeedID: "capture", AgentID: agent.ID, LegacyName: row.Name}
	if _, err = st.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{seed}); err == nil {
		t.Fatal("operator-owned definition taken")
	}
	got, err := st.GetAgentReflex(ctx, originalID)
	if err != nil || got.CreatedBy != "user" {
		t.Fatal("operator source changed", got, err)
	}
	row.ID = ""
	row.CreatedBy = "system"
	if _, err = st.InsertAgentReflex(ctx, row); err != nil {
		t.Fatal(err)
	}
	if _, err = st.BindExistingPluginReflexSeeds(ctx, "nanite.loom", []ExistingPluginReflexSeed{seed}); err == nil {
		t.Fatal("ambiguous source adopted")
	}
}
