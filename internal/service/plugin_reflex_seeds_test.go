package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func pluginSeedHistoricalFixture(t *testing.T, st *store.Store, slug, owner, name string) *store.AgentProfile {
	t.Helper()
	p := &store.AgentProfile{ID: "retained-" + slug, Slug: slug, Name: "Retained " + slug, SystemPrompt: "Private retained instructions"}
	if err := storetest.HistoricalProfile(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	id := "retained-reflex-" + slug
	tier := "plugin"
	if owner == "system" {
		tier = "system"
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_reflexes(id,agent_id,name,trigger_kind,trigger_spec,action_kind,action_spec,status,priority,fired_count,last_fired_at,created_at,created_by,opt_out_allowed,provenance_tier) VALUES(?,?,?,'predicate','{"kind":"tool_calls_window","window":1,"op":"=","value":0}','inject_reminder','{"body":"Private edited reminder"}','paused',77,7,'retained-fired','retained-created',?,1,?)`, id, p.ID, name, owner, tier); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_reflex_opt_outs(agent_id,reflex_id,created_at) VALUES(?,?,'retained-opt-out')`, p.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO plugin_reflex_seed_bindings(plugin_id,seed_id,agent_id,reflex_id) VALUES('bookmarks','remind',?,?),('bookmarks','deleted',?,NULL)`, p.ID, id, p.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

// Static catalog host settings author a private pin but supply no binding,
// receipt, instance or grant. A host UUID cannot authorize mutable seeds.
func pluginSeedStaticHost(t *testing.T, st *store.Store, slug string) store.AgentHostSettings {
	t.Helper()
	if err := installEmbeddedChatDefinition(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	embedded, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Slug: slug, Title: "Static catalog host", DefinitionRef: embedded.Ref.MeshRef(), Enabled: true, Settings: store.NativeHostSettings{Version: "1", Runtime: "api"}, Source: "private-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func pluginSeedHistorySnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	out := retainedDispatchSnapshot(t, st)
	query := `SELECT * FROM plugin_reflex_seed_bindings ORDER BY plugin_id,seed_id,agent_id`
	out[query] = immutableConfigSnapshot(t, st, query)
	return out
}
func assertPluginSeedHistoryUnchanged(t *testing.T, st *store.Store, before map[string][][]any) {
	t.Helper()
	if after := pluginSeedHistorySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("plugin seed lifecycle changed edited history, ownership, opt-outs, tombstones or actor authority")
	}
}
func pluginReminderSeed(slug string) ([]pluginapi.ReflexSeed, pluginapi.ReflexScope) {
	return []pluginapi.ReflexSeed{{ID: "remind", AgentSlug: slug, Reminder: "Search first", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}}}, pluginapi.ReflexScope{SeedIDs: []string{"remind"}, AgentSlugs: []string{slug}}
}

func TestImmutablePluginSeedServiceRefusesPreparationAndReloadWithoutEffects(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	historical := pluginSeedHistoricalFixture(t, st, "seed-agent", "plugin:bookmarks", "edited-seed")
	_ = pluginSeedStaticHost(t, st, "seed-agent")
	before := pluginSeedHistorySnapshot(t, st)
	r := NewPluginReflexSeeds(st)
	for _, slug := range []string{"seed-agent", "historical-only", "missing-agent"} {
		if slug == "historical-only" {
			p := &store.AgentProfile{ID: "retained-only-seed", Slug: slug, Name: "Retained only", SystemPrompt: "Private historical body"}
			if err := storetest.HistoricalProfile(t.Context(), st, p); err != nil {
				t.Fatal(err)
			}
			before = pluginSeedHistorySnapshot(t, st)
		}
		seeds, scope := pluginReminderSeed(slug)
		for attempt := 0; attempt < 2; attempt++ {
			if err := r.PreparePluginReflexSeeds("bookmarks", seeds, scope); !errors.Is(err, store.ErrImmutableAgentProfile) {
				t.Errorf("prepare %q=%v; want ErrImmutableAgentProfile", slug, err)
			}
			if len(r.owners) != 0 || len(*r.live.Load()) != 0 {
				t.Error("refused seed preparation staged or activated a source")
			}
			r.RemovePluginReflexSeeds("bookmarks")
			assertPluginSeedHistoryUnchanged(t, st, before)
		}
	}
	if rows, err := st.ListAgentReflexesForAgent(t.Context(), historical.ID, ""); rows != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("historical executable candidates=%+v,%v", rows, err)
	}
	assertPluginSeedHistoryUnchanged(t, st, before)
}

func TestPluginSeedDeclarationScopeRemainsStaticAndValidated(t *testing.T) {
	seeds, scope := pluginReminderSeed("static-target")
	validate := func(seeds []pluginapi.ReflexSeed, scope pluginapi.ReflexScope) error {
		raw, err := json.Marshal(scope)
		if err != nil {
			return err
		}
		_, err = pluginapi.ReflexScopeFor(pluginapi.Block{Registers: pluginapi.Registrations{ReflexSeeds: seeds}}, []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityReflexSeed, Metadata: raw}})
		return err
	}
	if err := validate(seeds, scope); err != nil {
		t.Fatalf("valid static declaration: %v", err)
	}
	badScope := scope
	badScope.AgentSlugs = []string{"other-target"}
	if err := validate(seeds, badScope); err == nil {
		t.Fatal("undeclared seed target accepted")
	}
	badScope = scope
	badScope.SeedIDs = []string{"different-seed"}
	if err := validate(seeds, badScope); err == nil {
		t.Fatal("undeclared seed identity accepted")
	}
	if err := validate(append(seeds, seeds[0]), scope); err == nil {
		t.Fatal("duplicate seed declaration accepted")
	}
}

func TestImmutablePluginSeedActivationCannotPublishPriorMutableLease(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	historical := pluginSeedHistoricalFixture(t, st, "seed-agent", "plugin:bookmarks", "edited-seed")
	before := pluginSeedHistorySnapshot(t, st)
	r := NewPluginReflexSeeds(st)
	// Already staged mutable IDs from a prior process state confer no pinned
	// execution authority after the cut. No seed binding is issued here.
	r.owners["bookmarks"] = pluginSeedLease{ids: []string{"retained-reflex-seed-agent"}}
	if err := r.ActivatePluginReflexSeeds("bookmarks"); !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Errorf("prior mutable seed activation=%v; want ErrImmutableAgentProfile", err)
	}
	if len(*r.live.Load()) != 0 || r.owners["bookmarks"].active {
		t.Error("prior mutable seed lease became live")
	}
	if rows, err := st.ListAgentReflexesForAgent(t.Context(), historical.ID, ""); rows != nil || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("retained source became executable=%+v,%v", rows, err)
	}
	r.RemovePluginReflexSeeds("bookmarks")
	assertPluginSeedHistoryUnchanged(t, st, before)
}
