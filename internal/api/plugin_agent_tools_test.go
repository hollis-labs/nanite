package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
)

func TestPluginAgentToolsRealReviewedChildAndPermissions(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	manager := mcp.NewManager()
	host := naniteplugin.NewHost(nil, naniteplugin.NewLogger("agent-tools-test"))
	host.SetMCPRegistrar(service.NewPluginToolRegistrar(manager, st))
	root := t.TempDir()
	directory := filepath.Join(root, "reviewed-tools")
	if checkErr := os.Mkdir(directory, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	path := writeAPIPluginBundle(t, directory, "reviewed-tools", "Reviewed tools", pluginapi.Block{LoadType: pluginapi.LoadOptIn})
	raw, err := os.ReadFile(path) // #nosec G304 -- private fixture returns this bundle path.
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := manifest.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	declaration.Tools = []manifest.Tool{{Name: "declared_echo", Description: "Echo approved input", Effect: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "declared_delete", Description: "Destructive action", Effect: "destructive", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	var encoded strings.Builder
	if checkErr := manifest.Encode(&encoded, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(path, []byte(encoded.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := naniteplugin.BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := naniteplugin.SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
	if len(loaded) != 1 || len(failures) != 0 {
		t.Fatalf("load %v %v", loaded, failures)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("reviewed-tools") })
	if len(manager.GetAllToolsUnfiltered()) != 2 || len(manager.GetAllTools()) != 0 {
		t.Fatal("manifest discovery/load default missing")
	}
	client := toolclient.New(manager, st, toolclient.DefaultConfig())
	agent := &store.AgentProfile{Name: "Tool caller", Slug: "tool-caller", SystemPrompt: "test"}
	if checkErr := st.CreateAgent(ctx, agent); checkErr != nil {
		t.Fatal(checkErr)
	}
	anchor, err := st.UpsertKnownTool(ctx, "anchor", "builtin", "available", "anchor")
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := st.GrantAgentTool(ctx, agent.ID, anchor, "explicit"); checkErr != nil {
		t.Fatal(checkErr)
	}
	manager.SetToolLoadPreferences(map[string]string{"declared_echo": "auto"})
	if _, checkErr := client.CallTool(ctx, agent.ID, "declared_echo", nil); checkErr == nil || !strings.Contains(checkErr.Error(), "permission denied") {
		t.Fatal("ungranted plugin tool executed")
	}
	known, err := st.GetKnownToolByName(ctx, "declared_echo")
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := st.GrantAgentTool(ctx, agent.ID, known.ID, "explicit"); checkErr != nil {
		t.Fatal(checkErr)
	}
	output, err := client.CallTool(mcp.WithSessionID(ctx, "host-session"), agent.ID, "declared_echo", map[string]any{"session_id": "argument-session"})
	if err != nil {
		t.Fatal(err)
	}
	var echoed struct {
		Name      string         `json:"tool_name"`
		SessionID string         `json:"session_id"`
		Arguments map[string]any `json:"arguments"`
	}
	if checkErr := json.Unmarshal([]byte(output), &echoed); checkErr != nil {
		t.Fatal(checkErr)
	}
	if echoed.Name != "declared_echo" || echoed.SessionID != "host-session" || echoed.Arguments["session_id"] != "argument-session" {
		t.Fatalf("SDK caller scope lost: %s", output)
	}
	tools := service.NewToolService(client, manager, st)
	meta, ok := tools.GetToolMeta(ctx, "declared_delete")
	if !ok || !meta.IsDestructive || meta.IsReadOnly || !meta.WriteDeclared {
		t.Fatal("permission engine lost reviewed destructive effect")
	}
	if _, checkErr := manager.ExecuteTool(ctx, "identity", nil); checkErr == nil {
		t.Fatal("child's undeclared tool exposed")
	}
	if checkErr := host.UnloadPlugin("reviewed-tools"); checkErr != nil {
		t.Fatal(checkErr)
	}
	unavailable, err := st.GetKnownToolByName(ctx, "declared_echo")
	if err != nil || unavailable.ID != known.ID || unavailable.Status != "unavailable" {
		t.Fatal("unload lost permission identity or availability")
	}
	if _, checkErr := client.CallTool(ctx, agent.ID, "declared_echo", nil); checkErr == nil {
		t.Fatal("unloaded tool executed")
	}
}
