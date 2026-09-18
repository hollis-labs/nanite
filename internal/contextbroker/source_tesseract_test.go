package contextbroker

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"

	gmcpserver "github.com/hollis-labs/go-mcp/server"
	gmcphttp "github.com/hollis-labs/go-mcp/transport/http"

	"github.com/hollis-labs/nanite/internal/mcp"
)

type recordingMCPCaller struct {
	server string
	tool   string
	input  map[string]any
	result string
	err    error
}

func (m *recordingMCPCaller) ExecuteTool(context.Context, string, map[string]any) (string, error) {
	return "", errors.New("unexpected uniform tool call")
}

func (m *recordingMCPCaller) ExecuteToolOnServer(ctx context.Context, server, tool string, input map[string]any) (string, error) {
	m.server, m.tool, m.input = server, tool, input
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return m.result, m.err
}

func TestTesseractSourceUsesContextPlanExecuteContract(t *testing.T) {
	caller := &recordingMCPCaller{result: `{
		"items":[
			{"namespace":"user/memory/decisions","key":"one","payload":{"summary":"first"}},
			{"namespace":"user/memory/notes","key":"two","payload_head":"partial","payload_truncated":true}
		],
		"manifest":{"items_returned":2,"tokens_estimate":10,"truncated":false},
		"rationale":"test"
	}`}
	source := NewTesseractSource(caller)
	items, err := source.Fetch(context.Background(), Intent{
		Type: IntentResumeTask, QueryText: "resume migration", Keywords: []string{"tesseract"}, Scope: "nanite",
	}, 128)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if caller.server != "tesseract" || caller.tool != "context_plan" {
		t.Fatalf("call = %s/%s", caller.server, caller.tool)
	}
	// max_items / max_tokens_estimate, not the pre-v0.10.0 budget_items /
	// budget_tokens — Tesseract refuses the retired names on context_plan
	// rather than ignoring them.
	wantArgs := map[string]any{
		"execute": true, "intent": "resume_task",
		"summary":   "resume migration tesseract scope nanite",
		"max_items": 2, "max_tokens_estimate": 128,
	}
	if !reflect.DeepEqual(caller.input, wantArgs) {
		t.Fatalf("args = %#v, want %#v", caller.input, wantArgs)
	}
	if len(items) != 2 || items[0].Source != "tesseract" || items[0].Key != "user/memory/decisions/one" {
		t.Fatalf("items = %+v", items)
	}
	if items[1].Content != "partial" || items[1].Metadata["payload_truncated"] != "true" {
		t.Fatalf("truncated item = %+v", items[1])
	}
}

func TestTesseractSourceRejectsStaleOrMalformedShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "not json", raw: "plain text"},
		{name: "retired records shape", raw: `{"records":[]}`},
		{name: "missing payload", raw: `{"items":[{"namespace":"user/memory/x","key":"k"}],"manifest":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &recordingMCPCaller{result: tc.raw}
			_, err := NewTesseractSource(caller).Fetch(context.Background(), Intent{}, 100)
			if err == nil {
				t.Fatal("expected schema error")
			}
			if caller.tool != "context_plan" {
				t.Fatalf("tool = %q, want context_plan", caller.tool)
			}
		})
	}
}

func TestTesseractSourcePropagatesMCPFailure(t *testing.T) {
	caller := &recordingMCPCaller{err: errors.New("offline")}
	_, err := NewTesseractSource(caller).Fetch(context.Background(), Intent{}, 100)
	if err == nil || err.Error() != "tesseract context_plan: offline" {
		t.Fatalf("err = %v", err)
	}
}

func TestTesseractSourcePropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewTesseractSource(&recordingMCPCaller{}).Fetch(ctx, Intent{}, 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context cancellation", err)
	}
}

func TestTesseractSourceThroughDiscoveredManagerRegistry(t *testing.T) {
	// Manager.AddHTTPServer now dials a real MCP streamable-HTTP session
	// (initialize included) rather than firing bare JSON-RPC POSTs the way
	// the former hand-rolled HTTPTransport did, so the fixture on the other
	// end needs to be a real MCP server too -- go-mcp/server plus
	// go-mcp/transport/http, not a hand-decoded JSON-RPC switch. This also
	// exercises real client/server interop between the two halves of the
	// CW-20260917-0017 migration, not just Nanite's client side in
	// isolation.
	var sawContextPlan bool
	fixture := gmcpserver.NewServer("tesseract-fixture", "0.0.0")
	fixture.RegisterTool(gmcpserver.Tool{
		Name:        "context_plan",
		Description: "Plan or execute a Tesseract context fetch.",
		InputSchema: gmcpserver.ObjectSchema(map[string]any{
			"execute": map[string]any{"type": "boolean"},
		}),
		ReadOnlyHint:   true,
		IdempotentHint: true,
		Handler: func(_ context.Context, args map[string]any) (any, error) {
			if args["execute"] != true {
				return nil, fmt.Errorf("wrong call")
			}
			sawContextPlan = true
			return `{"items":[{"namespace":"user/demo","key":"decision","payload":{"summary":"current"}}],"manifest":{"items_returned":1},"rationale":"test"}`, nil
		},
	})
	server := httptest.NewServer(gmcphttp.NewHandler(fixture, gmcphttp.HandlerOptions{}))
	defer server.Close()

	manager := mcp.NewManager()
	t.Cleanup(manager.Close)
	if err := manager.AddHTTPServer("tesseract", server.URL, mcp.TierPluginHTTP); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := manager.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	registered := manager.GetAllToolsUnfiltered()
	if len(registered) != 1 || registered[0].Name != "context_plan" {
		t.Fatalf("registered tools = %+v", registered)
	}
	items, err := NewTesseractSource(manager).Fetch(context.Background(), Intent{Type: IntentResumeTask}, 128)
	if err != nil || len(items) != 1 || !sawContextPlan {
		t.Fatalf("Fetch items=%+v err=%v sawContextPlan=%v", items, err, sawContextPlan)
	}
}
