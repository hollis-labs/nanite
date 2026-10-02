package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/plugin-sdk/manifest"
)

func TestPluginToolsPreserveRosterIdentityAcrossUnload(t *testing.T) {
	ctx := context.Background()
	st := newKnownToolsTestStore(t)
	manager := mcp.NewManager()
	registrar := NewPluginToolRegistrar(manager, st)
	declarations := []manifest.Tool{{Name: "reviewed_tool", Description: "Reviewed tool", Effect: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	child := subprocess.NewSubprocessPluginForTest("reader", nil)
	if checkErr := registrar.AddPluginTools("reader", declarations, "opt-in", child, nil); checkErr != nil {
		t.Fatal(checkErr)
	}
	known, err := st.GetKnownToolByName(ctx, "reviewed_tool")
	if err != nil || known.Status != "available" {
		t.Fatal("hidden tool missing from permission catalog")
	}
	agent := &store.AgentProfile{Name: "Reader", Slug: "reader", SystemPrompt: "test"}
	if checkErr := st.CreateAgent(ctx, agent); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := st.GrantAgentTool(ctx, agent.ID, known.ID, "explicit"); checkErr != nil {
		t.Fatal(checkErr)
	}
	if registrar.RemoveServersByPlugin("reader") != 1 {
		t.Fatal("namespace missing")
	}
	unavailable, err := st.GetKnownToolByName(ctx, "reviewed_tool")
	if err != nil || unavailable.Status != "unavailable" || unavailable.ID != known.ID {
		t.Fatal("unload deleted or left available roster row")
	}
	declarations[0].Description = "Upgraded tool"
	if checkErr := registrar.AddPluginTools("reader", declarations, "auto", child, nil); checkErr != nil {
		t.Fatal(checkErr)
	}
	upgraded, err := st.GetKnownToolByName(ctx, "reviewed_tool")
	if err != nil || upgraded.Status != "available" || upgraded.ID != known.ID {
		t.Fatal("reload replaced durable identity")
	}
	grants, err := st.ListAgentToolNames(ctx, agent.ID)
	if err != nil || len(grants) != 1 || grants[0] != "reviewed_tool" {
		t.Fatal("upgrade lost agent grant")
	}
}

func TestPluginToolPreferencesPublishPersistedMerges(t *testing.T) {
	st := newKnownToolsTestStore(t)
	if checkErr := st.Seed(context.Background()); checkErr != nil {
		t.Fatal(checkErr)
	}
	manager := mcp.NewManager()
	settings := newUserSettingsWithToolLoads(st, manager)
	var writers sync.WaitGroup
	errors := make(chan error, 8)
	for i := range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			_, err := settings.UpdateToolLoadPreferences(context.Background(), map[string]string{fmt.Sprintf("tool_%d", i): "opt-in"})
			errors <- err
		}()
	}
	writers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	persisted, err := settings.ToolLoadPreferences(context.Background())
	if err != nil || len(persisted) != 8 {
		t.Fatalf("lost preference merge: %v %v", persisted, err)
	}
	for name, value := range persisted {
		actual, source := manager.ToolLoadType(name)
		if actual != value || source != "user" {
			t.Fatalf("live filtering differs from persistence for %s", name)
		}
	}
	if _, checkErr := settings.UpdateToolLoadPreferences(context.Background(), map[string]string{"tool_0": ""}); checkErr != nil {
		t.Fatal(checkErr)
	}
	if actual, source := manager.ToolLoadType("tool_0"); actual != "auto" || source != "default" {
		t.Fatal("removed preference remained live")
	}
}
