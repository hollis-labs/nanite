package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type contextCallerFunc func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error)

func (f contextCallerFunc) CallHTTP(ctx context.Context, r *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
	return f(ctx, r)
}
func testContextScope() pluginapi.ContextScope {
	return pluginapi.ContextScope{SourceIDs: []string{"notes"}, SessionIDs: []string{"session-one"}}
}
func testContextDeclarations() []pluginapi.ContextSource {
	return []pluginapi.ContextSource{{ID: "notes"}}
}
func contextReply(content string) *sdkprocess.HTTPResponse {
	body, _ := json.Marshal(pluginapi.ContextResponse{Protocol: pluginapi.ContextProtocol, Items: []pluginapi.ContextItem{{Key: "n1", Content: content, Relevance: 1}}})
	return &sdkprocess.HTTPResponse{Status: 200, Body: body}
}

func TestPluginContextSourcesScopesBudgetsAndOwnership(t *testing.T) {
	registry := NewPluginContextSources()
	calls := 0
	var captured pluginapi.ContextRequest
	caller := contextCallerFunc(func(_ context.Context, request *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		calls++
		var err error
		captured, err = pluginapi.DecodeContextRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		return contextReply("Plugin notes"), nil
	})
	scope := testContextScope()
	if err := registry.add("notes-plugin", testContextDeclarations(), scope, caller); err != nil {
		t.Fatal(err)
	}
	scope.SessionIDs[0] = "other" // accepted authority is immutable
	intent := contextbroker.Intent{SessionID: "session-one", AgentID: "agent-one", Type: "write_code", Scope: "/private/project", QueryText: "private query", Keywords: []string{"private"}}
	items, err := registry.Fetch(context.Background(), intent, 500)
	if err != nil || len(items) != 1 || calls != 1 {
		t.Fatalf("fetch: %+v %v calls=%d", items, err, calls)
	}
	if captured.QueryText != "" || len(captured.Keywords) != 0 || captured.AgentID != "agent-one" || items[0].Source != "plugin/notes-plugin/notes" || items[0].TokenEstimate <= 0 {
		t.Fatalf("authority leaked: %+v %+v", captured, items)
	}
	intent.SessionID = "other"
	if items, err := registry.Fetch(context.Background(), intent, 500); err != nil || len(items) != 0 || calls != 1 {
		t.Fatal("out-of-scope call")
	}
	intent.SessionID = "session-one"
	if items, err := registry.Fetch(context.Background(), intent, 1); err != nil || len(items) != 0 {
		t.Fatal("oversized item bypassed token budget")
	}
	registry.RemovePluginContextSources("unrelated")
	if err := registry.add("notes-plugin", testContextDeclarations(), testContextScope(), caller); err == nil {
		t.Fatal("duplicate owner accepted")
	}
	registry.RemovePluginContextSources("notes-plugin")
	scope = testContextScope()
	scope.IncludeQuery = true
	if err := registry.add("notes-plugin", testContextDeclarations(), scope, caller); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Fetch(context.Background(), intent, 500); err != nil || captured.QueryText != intent.QueryText || len(captured.Keywords) != 1 {
		t.Fatal("reviewed query not forwarded")
	}
}

func TestPluginContextSourcesUnloadCancelsAndDiscards(t *testing.T) {
	registry := NewPluginContextSources()
	started := make(chan struct{})
	canceled := make(chan struct{})
	caller := contextCallerFunc(func(ctx context.Context, _ *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return contextReply("stale"), nil
	})
	if err := registry.add("notes-plugin", testContextDeclarations(), testContextScope(), caller); err != nil {
		t.Fatal(err)
	}
	finished := make(chan []contextbroker.ContextItem, 1)
	go func() {
		items, _ := registry.Fetch(context.Background(), contextbroker.Intent{SessionID: "session-one"}, 500)
		finished <- items
	}()
	<-started
	registry.RemovePluginContextSources("notes-plugin")
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("unload did not cancel active call")
	}
	select {
	case items := <-finished:
		if len(items) != 0 {
			t.Fatal("stale response published")
		}
	case <-time.After(time.Second):
		t.Fatal("retrieval did not finish")
	}
	if items, err := registry.Fetch(context.Background(), contextbroker.Intent{SessionID: "session-one"}, 500); err != nil || len(items) != 0 {
		t.Fatal("unloaded source retained")
	}
}

func TestPluginContextSourcesRejectChildMetadata(t *testing.T) {
	for _, body := range []string{`{"protocol":1,"items":[{"key":"n1","content":"x","relevance":1,"source":"memory"}]}`, `{"protocol":1,"items":[{"key":"n1","content":"x","relevance":1,"token_estimate":0}]}`, strings.Repeat("x", pluginapi.MaxContextBytes+1)} {
		registry := NewPluginContextSources()
		caller := contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
			return &sdkprocess.HTTPResponse{Status: 200, Body: []byte(body)}, nil
		})
		if err := registry.add("notes-plugin", testContextDeclarations(), testContextScope(), caller); err != nil {
			t.Fatal(err)
		}
		items, err := registry.Fetch(context.Background(), contextbroker.Intent{SessionID: "session-one"}, 500)
		if err != nil || len(items) != 0 {
			t.Fatal("malformed output entered context")
		}
	}
}

func TestPluginContextSourcesConcurrentLifecycle(t *testing.T) {
	registry := NewPluginContextSources()
	caller := contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		return contextReply("notes"), nil
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_, _ = registry.Fetch(context.Background(), contextbroker.Intent{SessionID: "session-one"}, 500)
			}
		}()
	}
	for range 100 {
		if err := registry.add("notes-plugin", testContextDeclarations(), testContextScope(), caller); err != nil {
			t.Fatal(err)
		}
		registry.RemovePluginContextSources("notes-plugin")
	}
	wg.Wait()
}

func TestPluginContextSourcesPromptSlotsAcrossDispatches(t *testing.T) {
	for _, flavor := range dispatchFlavors() {
		t.Run(flavor.Name, func(t *testing.T) {
			fixture := newInvariantsFixture(t)
			registry := NewPluginContextSources()
			scope := testContextScope()
			scope.SessionIDs = []string{fixture.session.ID}
			caller := contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				return contextReply("PLUGIN_CONTEXT_SENTINEL"), nil
			})
			if err := registry.add("notes-plugin", testContextDeclarations(), scope, caller); err != nil {
				t.Fatal(err)
			}
			fixture.client.ContextBroker = contextbroker.New(contextbroker.DefaultBudget(), registry)
			result := fixture.assembleWithCaller(t, flavor.Caller)
			if err := invariantStableSentShape(result); err != nil {
				t.Fatal(err)
			}
			if err := invariantUniversalAtPositionZero(result); err != nil {
				t.Fatal(err)
			}
			if err := invariantCacheMarkerPriority(result); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, block := range result.Blocks {
				if strings.Contains(block.Content, "PLUGIN_CONTEXT_SENTINEL") {
					found = true
					if block.SlotName != "context" {
						t.Fatalf("plugin chose slot %s", block.SlotName)
					}
				}
			}
			if !found {
				t.Fatal("plugin contribution absent from prompt")
			}
		})
	}
}

func TestPluginContextSourcesCallerCancellation(t *testing.T) {
	registry := NewPluginContextSources()
	caller := contextCallerFunc(func(ctx context.Context, _ *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		<-ctx.Done()
		return contextReply("late"), nil
	})
	if err := registry.add("notes-plugin", testContextDeclarations(), testContextScope(), caller); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	items, err := registry.Fetch(ctx, contextbroker.Intent{SessionID: "session-one"}, 500)
	if err == nil || len(items) != 0 {
		t.Fatal("canceled retrieval published output")
	}
}
