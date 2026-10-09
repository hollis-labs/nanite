package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginContextSourcesApprovedSDKLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	directory := filepath.Join(root, "context-plugin")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := writeAPIPluginBundle(t, directory, "context-plugin", "Context plugin", pluginapi.Block{})
	declarationFile, err := os.Open(path) // #nosec G304 -- fixture-generated manifest beneath t.TempDir, not external input.
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := manifest.Decode(declarationFile)
	_ = declarationFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	block := pluginapi.Block{Registers: pluginapi.Registrations{ContextSources: []pluginapi.ContextSource{{ID: "notes"}}}}
	declaration.Nanite, err = pluginapi.EncodeBlock(block)
	if err != nil {
		t.Fatal(err)
	}
	scope := pluginapi.ContextScope{SourceIDs: []string{"notes"}, SessionIDs: []string{"allowed-session"}}
	raw, _ := json.Marshal(scope)
	declaration.Capabilities = []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityContextSource, Metadata: raw}}
	var manifestText strings.Builder
	if checkErr := manifest.Encode(&manifestText, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(path, []byte(manifestText.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := naniteplugin.BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Capabilities) != 1 || review.Capabilities[0].Name != pluginapi.CapabilityContextSource {
		t.Fatal("scope absent from review")
	}
	if checkErr := naniteplugin.SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	host := naniteplugin.NewHost(nil, naniteplugin.NewLogger("context-test"))
	registry := service.NewPluginContextSources()
	host.SetContextSourceRegistrar(registry)
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
	if len(loaded) != 1 || len(failures) != 0 {
		t.Fatalf("load: %v %v", loaded, failures)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("context-plugin") })
	broker := contextbroker.New(contextbroker.DefaultBudget(), registry)
	packet, err := broker.Fetch(ctx, contextbroker.Intent{SessionID: "allowed-session", AgentID: "host-agent", Type: "write_code", Scope: "/secret/project", QueryText: "private user query", Keywords: []string{"secret"}})
	if err != nil || len(packet.Items) != 1 {
		t.Fatalf("packet: %+v %v", packet, err)
	}
	var captured pluginapi.ContextRequest
	if err := json.Unmarshal([]byte(packet.Items[0].Content), &captured); err != nil {
		t.Fatal(err)
	}
	if captured.SessionID != "allowed-session" || captured.AgentID != "host-agent" || captured.QueryText != "" || len(captured.Keywords) != 0 || packet.Items[0].Source != "plugin/context-plugin/notes" {
		t.Fatalf("host identity/scope lost: %+v %+v", captured, packet.Items)
	}
	if packet, err := broker.Fetch(ctx, contextbroker.Intent{SessionID: "other-session"}); err != nil || len(packet.Items) != 0 {
		t.Fatal("out-of-scope context exposed")
	}
	if err := host.UnloadPlugin("context-plugin"); err != nil {
		t.Fatal(err)
	}
	if packet, err := broker.Fetch(ctx, contextbroker.Intent{SessionID: "allowed-session"}); err != nil || len(packet.Items) != 0 {
		t.Fatal("unloaded context retained")
	}
	// A host without the extension point refuses and rolls back all registrations.
	unavailable := naniteplugin.NewHost(nil, naniteplugin.NewLogger("context-unavailable"))
	rejected, errors := naniteplugin.LoadDiscovered(unavailable, discovered)
	if len(rejected) != 0 || len(errors) != 1 || len(unavailable.ListPlugins()) != 0 || unavailable.GetManifest("context-plugin") != nil {
		t.Fatal("failed context registration left a partial plugin")
	}
	// The unchanged approved bundle can load again without retaining old sources.
	loaded, failures = naniteplugin.LoadDiscovered(host, discovered)
	if len(loaded) != 1 || len(failures) != 0 {
		t.Fatalf("reload: %v %v", loaded, failures)
	}
	if packet, err := broker.Fetch(ctx, contextbroker.Intent{SessionID: "allowed-session"}); err != nil || len(packet.Items) != 1 {
		t.Fatal("reload failed")
	}
}
