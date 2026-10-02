package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigration169BindingsReverseWithoutDeletingDefinitions(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	agent := &AgentProfile{ID: "binding-migration-agent", Name: "Migration target", Slug: "binding-migration-agent", SystemPrompt: "test"}
	if checkErr := st.CreateAgent(ctx, agent); checkErr != nil {
		t.Fatal(checkErr)
	}
	seed := PluginReflexSeed{SeedID: "remind", Definition: AgentReflex{ID: "binding-migration-reflex", AgentID: agent.ID, Name: "bookmarks:remind", TriggerKind: ReflexTriggerPredicate, TriggerSpec: `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`, ActionKind: ReflexActionInjectReminder, ActionSpec: `{"body":"keep"}`, CreatedBy: "plugin:bookmarks", ProvenanceTier: "plugin", OptOutAllowed: true}}
	if _, checkErr := st.BindPluginReflexSeeds(ctx, "bookmarks", []PluginReflexSeed{seed}); checkErr != nil {
		t.Fatal(checkErr)
	}
	migrationFiles, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, st.DB, migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := provider.DownTo(ctx, 168); checkErr != nil {
		t.Fatal(checkErr)
	}
	row, err := st.GetAgentReflex(ctx, seed.Definition.ID)
	if err != nil || row.ActionSpec != seed.Definition.ActionSpec {
		t.Fatalf("down removed definition: %+v %v", row, err)
	}
	if _, checkErr := provider.Up(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	row, err = st.GetAgentReflex(ctx, seed.Definition.ID)
	if err != nil || row.ActionSpec != seed.Definition.ActionSpec {
		t.Fatalf("up changed definition: %+v %v", row, err)
	}
	var count int
	if checkErr := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_reflex_seed_bindings`).Scan(&count); checkErr != nil || count != 0 {
		t.Fatalf("migration seeded app data: %v %d", checkErr, count)
	}
	if _, checkErr := st.BindPluginReflexSeeds(ctx, "bookmarks", []PluginReflexSeed{seed}); checkErr == nil {
		t.Fatal("lost binding silently overwrote existing definition")
	}
}
