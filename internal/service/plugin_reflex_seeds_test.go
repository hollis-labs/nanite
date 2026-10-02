package service

import (
	"context"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginReflexSeedsLifecycleAndConcurrentRevocation(t *testing.T) {
	st := newConfigTestStore(t)
	ctx := context.Background()
	agent := &store.AgentProfile{Slug: "seed-agent", Name: "Seed agent", SystemPrompt: "test"}
	if checkErr := st.CreateAgent(ctx, agent); checkErr != nil {
		t.Fatal(checkErr)
	}
	r := NewPluginReflexSeeds(st)
	seeds := []pluginapi.ReflexSeed{{ID: "remind", AgentSlug: agent.Slug, Reminder: "Search first", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 1, Op: "=", Value: 0}}}
	scope := pluginapi.ReflexScope{SeedIDs: []string{"remind"}, AgentSlugs: []string{agent.Slug}}
	if checkErr := r.PreparePluginReflexSeeds("bookmarks", seeds, scope); checkErr != nil {
		t.Fatal(checkErr)
	}
	assertCount := func(want int) {
		t.Helper()
		rows, err := st.ListAgentReflexesForAgent(ctx, agent.ID, "")
		if err != nil || len(rows) != want {
			t.Fatalf("candidates=%+v error=%v want=%d", rows, err, want)
		}
	}
	assertCount(0)
	if checkErr := r.ActivatePluginReflexSeeds("bookmarks"); checkErr != nil {
		t.Fatal(checkErr)
	}
	assertCount(1)
	rows, err := st.ListAllAgentReflexes(ctx, agent.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("catalog: %+v %v", rows, err)
	}
	id := rows[0].ID
	if rows[0].CreatedBy != "plugin:bookmarks" || rows[0].ProvenanceTier != "plugin" || rows[0].ActionKind != store.ReflexActionInjectReminder || !rows[0].OptOutAllowed {
		t.Fatalf("authority changed: %+v", rows[0])
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = st.ListAgentReflexesForAgent(ctx, agent.ID, "")
		}
	}()
	r.RemovePluginReflexSeeds("bookmarks")
	wg.Wait()
	assertCount(0)
	rows, err = st.ListAllAgentReflexes(ctx, agent.ID)
	if err != nil || len(rows) != 1 || rows[0].Status != store.ReflexStatusActive {
		t.Fatalf("unload mutated durable status: %+v %v", rows, err)
	}
	if checkErr := r.PreparePluginReflexSeeds("bookmarks", seeds, scope); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := r.ActivatePluginReflexSeeds("bookmarks"); checkErr != nil {
		t.Fatal(checkErr)
	}
	rows, err = st.ListAgentReflexesForAgent(ctx, agent.ID, "")
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("reload changed identity: %+v %v", rows, err)
	}
	r.RemovePluginReflexSeeds("bookmarks")
	seeds[0].AgentSlug = "missing-agent"
	scope.AgentSlugs = []string{"missing-agent"}
	if checkErr := r.PreparePluginReflexSeeds("bookmarks", seeds, scope); checkErr == nil {
		t.Fatal("missing agent silently accepted")
	}
	assertCount(0)
}
