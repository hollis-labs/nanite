package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sdkplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type manifestToolFixture struct {
	calls   int
	request *sdkprocess.MCPCallRequest
	result  sdkprocess.MCPCallResult
}

func (f *manifestToolFixture) CallTool(_ context.Context, request *sdkprocess.MCPCallRequest) (*sdkprocess.MCPCallResult, error) {
	f.calls++
	f.request = request
	return &f.result, nil
}

type manifestEnvelopeFixture struct {
	session   string
	envelopes []sdkplugin.EnvelopeOut
}

func (f *manifestEnvelopeFixture) Deliver(session string, envelopes []sdkplugin.EnvelopeOut) bool {
	f.session = session
	f.envelopes = envelopes
	return true
}
func declaredFixtureTool(name, effect string) manifest.Tool {
	readOnly, destructive, _ := pluginapi.ToolEffectHints(effect)
	return manifest.Tool{Name: name, Description: "Reviewed tool", Effect: effect, InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`), Annotations: &manifest.ToolAnnotations{ReadOnlyHint: &readOnly, DestructiveHint: &destructive}}
}

func TestManifestToolsUseOnlyReviewedDeclarationsAndCanonicalCalls(t *testing.T) {
	fixture := &manifestToolFixture{result: sdkprocess.MCPCallResult{Content: json.RawMessage(`{"answer":"structured"}`), IsError: true, Envelopes: []sdkplugin.EnvelopeOut{{Type: "reviewed-card"}}}}
	consumer := &manifestEnvelopeFixture{}
	declarations := []manifest.Tool{declaredFixtureTool("reviewed_read", pluginapi.ToolEffectRead)}
	transport, err := newManifestPluginTransport(fixture, declarations, consumer)
	if err != nil {
		t.Fatal(err)
	}
	declarations[0].Description = "changed after acceptance"
	tools, err := transport.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Description != "Reviewed tool" || tools[0].Annotations["readOnlyHint"] != true || fixture.calls != 0 {
		t.Fatal("discovery consulted child or changed accepted metadata")
	}
	tools[0].InputSchema["type"] = "array"
	tools[0].Annotations["readOnlyHint"] = false
	original, err := transport.ListTools(context.Background())
	if err != nil || original[0].InputSchema["type"] != "object" || original[0].Annotations["readOnlyHint"] != true {
		t.Fatal("metadata escaped by reference")
	}
	if _, checkErr := transport.CallTool(context.Background(), "invented", nil); checkErr == nil || fixture.calls != 0 {
		t.Fatal("undeclared tool reached child")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, checkErr := transport.CallTool(canceled, "reviewed_read", nil); !errors.Is(checkErr, context.Canceled) || fixture.calls != 0 {
		t.Fatal("canceled call reached child")
	}
	result, err := transport.CallTool(WithSessionID(context.Background(), "caller-session"), "reviewed_read", map[string]any{"session_id": "untrusted-argument"})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.request.ToolName != "reviewed_read" || fixture.request.SessionID != "caller-session" || fixture.request.Arguments["session_id"] != "untrusted-argument" {
		t.Fatalf("canonical call: %+v", fixture.request)
	}
	if !result.IsError || result.Content[0].Text != `{"answer":"structured"}` || consumer.session != "caller-session" || len(consumer.envelopes) != 1 {
		t.Fatal("SDK error/output/envelopes lost")
	}
	fixture.result.Content = json.RawMessage(`[{"type":"text","text":"text"},{"type":"image","data":"kept"}]`)
	result, err = transport.CallTool(context.Background(), "reviewed_read", nil)
	if err != nil || len(result.Content) != 2 || result.Content[0].Text != "text" || result.Content[1].Text != `{"type":"image","data":"kept"}` {
		t.Fatal("content blocks lost")
	}
}

func TestManifestToolLoadDefaultsOwnershipAndUnload(t *testing.T) {
	manager := NewManager()
	child := subprocess.NewSubprocessPluginForTest("reader", nil)
	declarations := []manifest.Tool{declaredFixtureTool("reviewed_read", pluginapi.ToolEffectRead), declaredFixtureTool("reviewed_delete", pluginapi.ToolEffectDestructive)}
	if checkErr := manager.AddPluginTools("reader", declarations, "opt-in", child, nil); checkErr != nil {
		t.Fatal(checkErr)
	}
	if len(manager.GetAllTools()) != 0 || len(manager.GetAllToolsUnfiltered()) != 2 {
		t.Fatal("opt-in tools exposed or missing from catalog")
	}
	if _, checkErr := manager.ExecuteTool(context.Background(), "reviewed_read", nil); checkErr == nil {
		t.Fatal("hidden tool executed")
	}
	if _, checkErr := manager.ExecuteToolOnServer(context.Background(), "plugin_reader", "reviewed_read", nil); checkErr == nil || !strings.Contains(checkErr.Error(), "opt-in") {
		t.Fatal("explicit server call bypassed load preference")
	}
	hidden, checkErr := manager.DiscoverServerTools(context.Background(), "plugin_reader")
	if checkErr != nil || len(hidden) != 0 {
		t.Fatal("agent server discovery exposed opt-in tools")
	}
	if value, source := manager.ToolLoadType("reviewed_read"); value != "opt-in" || source != "manifest" {
		t.Fatal("manifest preference missing")
	}
	preferences := map[string]string{"reviewed_read": "auto"}
	manager.SetToolLoadPreferences(preferences)
	preferences["reviewed_delete"] = "auto"
	if len(manager.GetAllTools()) != 1 {
		t.Fatal("user preferences did not override defaults or leaked by reference")
	}
	read, destructive, ok := manager.ToolBehavior("reviewed_delete")
	if !ok || read || !destructive {
		t.Fatal("destructive declaration lost")
	}
	if checkErr := manager.AddPluginTools("intruder", declarations, "auto", subprocess.NewSubprocessPluginForTest("intruder", nil), nil); checkErr == nil {
		t.Fatal("another owner stole declared tool")
	}
	if len(manager.PluginToolDefinitions("intruder")) != 0 {
		t.Fatal("collision left partial namespace")
	}
	if removed := manager.RemoveServersByPlugin("reader"); removed != 1 || len(manager.GetAllToolsUnfiltered()) != 0 {
		t.Fatal("unload left tools")
	}
	if _, checkErr := manager.ExecuteTool(context.Background(), "reviewed_read", nil); checkErr == nil {
		t.Fatal("unloaded tool executed")
	}
	if checkErr := manager.AddPluginTools("reader", declarations, "auto", child, nil); checkErr != nil {
		t.Fatal(checkErr)
	}
}

func TestManifestToolPreferencesConcurrentPublication(t *testing.T) {
	manager := NewManager()
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 100 {
				value, _ := manager.ToolLoadType("changing")
				if value != "auto" && value != "opt-in" {
					t.Errorf("invalid live preference: %s", value)
				}
			}
		}()
	}
	for i := range 100 {
		value := "auto"
		if i%2 == 0 {
			value = "opt-in"
		}
		manager.SetToolLoadPreferences(map[string]string{"changing": value})
	}
	readers.Wait()
	// Discovery holds the registry mutex while querying servers. Publishing a
	// local preference must not wait on an unrelated subprocess/network call.
	manager.mu.Lock()
	published := make(chan struct{})
	go func() { manager.SetToolLoadPreferences(map[string]string{"changing": "auto"}); close(published) }()
	select {
	case <-published:
		manager.mu.Unlock()
	case <-time.After(time.Second):
		manager.mu.Unlock()
		<-published
		t.Fatal("preference publication waited on discovery")
	}
}
