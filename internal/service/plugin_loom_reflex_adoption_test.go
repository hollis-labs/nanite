package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestLoomReflexAdoptionWaitsForActivationAndPreservesDeletion(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	agents := map[string]*store.AgentProfile{}
	for _, slug := range []string{"loom-curator", "loom-weaver"} {
		agent := &store.AgentProfile{Slug: slug, Name: slug, SystemPrompt: "test"}
		if err := st.CreateAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
		agents[slug] = agent
	}
	seeds := []pluginapi.ReflexSeed{
		{ID: "check-before-answer", AgentSlug: "loom-weaver", Reminder: "default check", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}},
		{ID: "weaver-capture-on-discovery", AgentSlug: "loom-weaver", Reminder: "default capture", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}},
		{ID: "curator-capture-on-discovery", AgentSlug: "loom-curator", Reminder: "default capture", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}},
	}
	scope := pluginapi.ReflexScope{SeedIDs: []string{"check-before-answer", "weaver-capture-on-discovery", "curator-capture-on-discovery"}, AgentSlugs: []string{"loom-curator", "loom-weaver"}}
	first, err := st.InsertAgentReflex(ctx, store.AgentReflex{AgentID: agents["loom-weaver"].ID, Name: "check_before_answer", TriggerKind: store.ReflexTriggerPredicate, TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`, ActionKind: store.ReflexActionInjectReminder, ActionSpec: `{"body":"edited check"}`, CreatedBy: "system", OptOutAllowed: true, FiredCount: 7})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.InsertAgentReflex(ctx, store.AgentReflex{AgentID: agents["loom-weaver"].ID, Name: "capture_on_discovery", TriggerKind: store.ReflexTriggerPredicate, TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`, ActionKind: store.ReflexActionInjectReminder, ActionSpec: `{"body":"edited capture"}`, CreatedBy: "system", OptOutAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	r := NewPluginReflexSeeds(st)
	if err = r.PreparePluginReflexSeeds("nanite.loom", seeds, scope); err != nil {
		t.Fatal(err)
	}
	source, err := st.GetAgentReflex(ctx, first)
	if err != nil || source.CreatedBy != "system" {
		t.Fatal("prepare transferred source", source, err)
	}
	var count int
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_reflex_seed_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("prepare committed bindings", count, err)
	}
	r.RemovePluginReflexSeeds("nanite.loom")
	if err = r.PreparePluginReflexSeeds("nanite.loom", seeds, scope); err != nil {
		t.Fatal(err)
	}
	if err = r.ActivatePluginReflexSeeds("nanite.loom"); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListAgentReflexesForAgent(ctx, agents["loom-weaver"].ID, "")
	if err != nil || len(rows) != 2 {
		t.Fatal("activated sources missing", rows, err)
	}
	source, err = st.GetAgentReflex(ctx, first)
	if err != nil || source.ActionSpec != `{"body":"edited check"}` || source.FiredCount != 7 || source.CreatedBy != "plugin:nanite.loom" {
		t.Fatal("adoption lost source", source, err)
	}
	rows, err = st.ListAgentReflexesForAgent(ctx, agents["loom-curator"].ID, "")
	if err != nil || len(rows) != 0 {
		t.Fatal("missing legacy source recreated", rows, err)
	}
	r.RemovePluginReflexSeeds("nanite.loom")
	rows, err = st.ListAgentReflexesForAgent(ctx, agents["loom-weaver"].ID, "")
	if err != nil || len(rows) != 0 {
		t.Fatal("unloaded source executed", rows, err)
	}
	if err = st.DeleteAgentReflex(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err = r.PreparePluginReflexSeeds("nanite.loom", seeds, scope); err != nil {
		t.Fatal(err)
	}
	if err = r.ActivatePluginReflexSeeds("nanite.loom"); err != nil {
		t.Fatal(err)
	}
	rows, err = st.ListAgentReflexesForAgent(ctx, agents["loom-weaver"].ID, "")
	if err != nil || len(rows) != 1 || rows[0].ID != first {
		t.Fatal("reload recreated deleted source", rows, err)
	}
}
