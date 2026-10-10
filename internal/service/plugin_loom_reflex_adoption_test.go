package service

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func loomPilotSeeds() ([]pluginapi.ReflexSeed, pluginapi.ReflexScope) {
	seeds := []pluginapi.ReflexSeed{
		{ID: "check-before-answer", AgentSlug: "loom-weaver", Reminder: "Default check", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}},
		{ID: "weaver-capture-on-discovery", AgentSlug: "loom-weaver", Reminder: "Default capture", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}},
		{ID: "curator-capture-on-discovery", AgentSlug: "loom-curator", Reminder: "Default capture", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}},
	}
	return seeds, pluginapi.ReflexScope{SeedIDs: []string{"check-before-answer", "weaver-capture-on-discovery", "curator-capture-on-discovery"}, AgentSlugs: []string{"loom-curator", "loom-weaver"}}
}
func TestImmutableLoomSeedServiceCannotStageHistoricalAdoption(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	weaver := pluginSeedHistoricalFixture(t, st, "loom-weaver", "system", "check_before_answer")
	curator := pluginSeedHistoricalFixture(t, st, "loom-curator", "system", "capture_on_discovery")
	_ = pluginSeedStaticHost(t, st, "loom-weaver")
	_ = pluginSeedStaticHost(t, st, "loom-curator")
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO plugin_reflex_seed_bindings(plugin_id,seed_id,agent_id,reflex_id) VALUES('nanite.loom','weaver-capture-on-discovery',?,NULL),('nanite.loom','curator-capture-on-discovery',?,NULL)`, weaver.ID, curator.ID); err != nil {
		t.Fatal(err)
	}
	before := pluginSeedHistorySnapshot(t, st)
	seeds, scope := loomPilotSeeds()
	r := NewPluginReflexSeeds(st)
	if err := r.PreparePluginReflexSeeds("nanite.loom", seeds, scope); !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Errorf("Loom prepare=%v; want ErrImmutableAgentProfile", err)
	}
	if len(r.owners) != 0 || len(*r.live.Load()) != 0 {
		t.Error("Loom preparation staged mutable source ownership")
	}
	r.RemovePluginReflexSeeds("nanite.loom")
	assertPluginSeedHistoryUnchanged(t, st, before)
}

func TestImmutableLoomSeedActivationRefusesPriorStagedLeaseAndPreservesTombstones(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	weaver := pluginSeedHistoricalFixture(t, st, "loom-weaver", "system", "check_before_answer")
	before := pluginSeedHistorySnapshot(t, st)
	r := NewPluginReflexSeeds(st)
	// A process-local lease retained from before the immutable cut is not
	// permission to transfer ownership or replay an operator deletion.
	r.owners["nanite.loom"] = pluginSeedLease{existing: []store.ExistingPluginReflexSeed{{SeedID: "check-before-answer", AgentID: weaver.ID, LegacyName: "check_before_answer"}, {SeedID: "deleted", AgentID: weaver.ID, LegacyName: "capture_on_discovery"}}}
	for attempt := 0; attempt < 2; attempt++ {
		if err := r.ActivatePluginReflexSeeds("nanite.loom"); !errors.Is(err, store.ErrImmutableAgentProfile) {
			t.Fatalf("Loom activate=%v; want ErrImmutableAgentProfile", err)
		}
		if len(*r.live.Load()) != 0 || r.owners["nanite.loom"].active {
			t.Fatal("refused adoption became live")
		}
		assertPluginSeedHistoryUnchanged(t, st, before)
	}
	r.RemovePluginReflexSeeds("nanite.loom")
	if err := r.ActivatePluginReflexSeeds("nanite.loom"); err == nil {
		t.Fatal("removed lease activated")
	}
	assertPluginSeedHistoryUnchanged(t, st, before)
}
