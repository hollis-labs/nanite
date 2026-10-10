package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type recordingCandidateTransport struct {
	sdk.Transport
	writes chan []byte
}

func (t recordingCandidateTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &recordingCandidateConnection{Connection: connection, writes: t.writes}, nil
}

type recordingCandidateConnection struct {
	sdk.Connection
	writes chan []byte
}

func (c *recordingCandidateConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	raw, err := jsonrpc.EncodeMessage(message)
	if err != nil {
		return err
	}
	select {
	case c.writes <- raw:
	default:
	}
	return c.Connection.Write(ctx, message)
}
func candidateStdioClient(t *testing.T, adapter *Stdio) (*sdk.ClientSession, chan []byte) {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	writes := make(chan []byte, 32)
	serverSession, err := adapter.server.Connect(ctx, recordingCandidateTransport{Transport: serverTransport, writes: writes}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, writes
}
func candidateProxyForOwner(t *testing.T, owner ExecutionOwner) *Proxy {
	t.Helper()
	credentials, _, _ := fixtureCredentials(t, 1)
	token := issueFixture(t, credentials, "one")
	server := httptest.NewServer(fixtureHandler(t, credentials, owner))
	t.Cleanup(server.Close)
	proxy, err := NewProxy(server.URL, token, TransportLimits{MaxRequestBytes: 2048, MaxResponseBytes: 4096, MaxCallDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)
	return proxy
}
func stdioFixtureTool() ToolDefinition {
	return ToolDefinition{Tool: mcp.Tool{Name: "reviewed", Description: "Accepted declaration", InputSchema: map[string]any{"type": "object"}, Annotations: map[string]any{"title": "Reviewed title", "openWorldHint": false}}, Effect: "write", Binding: "binding-1"}
}
func TestCandidateStdioPreservesWireOmissionsAndPinnedHostDispatch(t *testing.T) {
	var drift atomic.Bool
	var calls atomic.Int32
	tool := stdioFixtureTool()
	owner := &ownerFixture{catalog: func(context.Context, VerifiedCaller) (Catalog, error) {
		revision := "review-1"
		if drift.Load() {
			revision = "review-2"
		}
		return Catalog{Version: CandidateVersion, Revision: revision, Tools: []ToolDefinition{tool}}, nil
	}, call: func(ctx context.Context, caller VerifiedCaller, request CallRequest) (*mcp.ToolResult, error) {
		calls.Add(1)
		if caller.ActorID != "verified-one" || caller.SessionID != "session-one" || request.Binding != "binding-1" || request.OperationKey != "host-operation" {
			t.Error("lost verified host dispatch inputs")
		}
		if drift.Load() {
			return nil, subprocess.ErrStaleBinding
		}
		return &mcp.ToolResult{IsError: true, Content: []mcp.ToolContent{{Type: "text", Text: "plugin business refusal"}}}, nil
	}}
	adapter, err := NewStdio(context.Background(), candidateProxyForOwner(t, owner), StdioLimits{MaxLineBytes: 4096, MaxCancelDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client, writes := candidateStdioClient(t, adapter)
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "reviewed" {
		t.Fatal("candidate exposed substitute inventory", listed, err)
	}
	var wireAnnotations map[string]any
	for len(writes) > 0 {
		raw := <-writes
		var response struct {
			Result struct {
				Tools []mcp.Tool `json:"tools"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &response) == nil && len(response.Result.Tools) != 0 {
			wireAnnotations = response.Result.Tools[0].Annotations
		}
	}
	if wireAnnotations["title"] != "Reviewed title" || wireAnnotations["openWorldHint"] != false {
		t.Fatal("lost accepted annotations", wireAnnotations)
	}
	for _, absent := range []string{"readOnlyHint", "idempotentHint", "destructiveHint"} {
		if _, present := wireAnnotations[absent]; present {
			t.Fatal("stdio synthesized omitted hint", absent)
		}
	}
	request := &sdk.CallToolParams{Name: "reviewed", Arguments: map[string]any{}, Meta: sdk.Meta{CandidateOperationKeyMeta: "host-operation"}}
	result, err := client.CallTool(context.Background(), request)
	if err != nil || !result.IsError || len(result.Content) != 1 || result.Content[0].(*sdk.TextContent).Text != "plugin business refusal" {
		t.Fatal("stdio changed business outcome", result, err)
	}
	drift.Store(true)
	if _, err := client.ListTools(context.Background(), nil); err == nil {
		t.Fatal("changed catalog refreshed automatically")
	}
	if _, err := client.CallTool(context.Background(), request); err == nil {
		t.Fatal("old binding retargeted")
	}
	if calls.Load() != 2 {
		t.Fatal("mutation retried")
	}
}

func TestCandidateStdioMissingAuthorityIsTypedUnavailable(t *testing.T) {
	limits := StdioLimits{MaxLineBytes: 4096, MaxCancelDuration: time.Second}
	if _, err := NewStdio(context.Background(), nil, limits); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal(err)
	}
	if _, err := NewStdio(context.Background(), candidateProxyForOwner(t, nil), limits); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatal("missing execution owner became empty catalog", err)
	}
}

func TestCandidateStdioCancellationTargetsSameRequestWithoutOutcomeClaim(t *testing.T) {
	credentials, _, _ := fixtureCredentials(t, 1)
	token := issueFixture(t, credentials, "one")
	started := make(chan string, 1)
	cancels := make(chan string, 1)
	owner := &ownerFixture{call: func(ctx context.Context, _ VerifiedCaller, request CallRequest) (*mcp.ToolResult, error) {
		started <- request.RequestID
		<-ctx.Done()
		return nil, ErrUnknownOutcome
	}}
	handler := fixtureHandler(t, credentials, owner)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/mcp/v1/cancel" {
			var request CancelRequest
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("invalid cancellation")
			}
			cancels <- request.RequestID
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"version": CandidateVersion, "canceled": false})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	proxy, err := NewProxy(server.URL, token, TransportLimits{MaxRequestBytes: 2048, MaxResponseBytes: 4096, MaxCallDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	adapter := &Stdio{proxy: proxy, limits: StdioLimits{MaxCancelDuration: time.Second}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcome := make(chan error, 1)
	go func() {
		result, err := adapter.handler(stdioFixtureTool())(ctx, &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)}})
		if result != nil {
			outcome <- errors.New("canceled mutation claimed successful result")
			return
		}
		outcome <- err
	}()
	var id string
	select {
	case id = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("call never dispatched")
	}
	cancel()
	select {
	case err := <-outcome:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not return")
	}
	select {
	case canceled := <-cancels:
		if canceled != id {
			t.Fatal("cancel retargeted")
		}
	case <-time.After(time.Second):
		t.Fatal("exact request cancellation not sent")
	}
}

func TestCandidateStdioRefusesPolicyFilteredInventoryDrift(t *testing.T) {
	var hidden atomic.Bool
	owner := &ownerFixture{catalog: func(context.Context, VerifiedCaller) (Catalog, error) {
		tools := []ToolDefinition{stdioFixtureTool()}
		if hidden.Load() {
			tools = []ToolDefinition{}
		}
		return Catalog{Version: CandidateVersion, Revision: "unchanged-definitions", Tools: tools}, nil
	}}
	adapter, err := NewStdio(context.Background(), candidateProxyForOwner(t, owner), StdioLimits{MaxLineBytes: 4096, MaxCancelDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client, _ := candidateStdioClient(t, adapter)
	hidden.Store(true)
	if _, err := client.ListTools(context.Background(), nil); err == nil {
		t.Fatal("stdio exposed withdrawn actor inventory")
	}
}
