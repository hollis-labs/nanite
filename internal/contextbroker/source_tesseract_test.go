package contextbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

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
	var sawContextPlan bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request mcp.JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "tools/list":
			payload, _ := json.Marshal(map[string]any{"tools": []map[string]any{{
				"name": "context_plan", "description": "Plan or execute a Tesseract context fetch.",
				"inputSchema": map[string]any{"type": "object"},
			}}})
			_ = json.NewEncoder(w).Encode(mcp.JSONRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: payload})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			raw, _ := json.Marshal(request.Params)
			_ = json.Unmarshal(raw, &params)
			if params.Name != "context_plan" || params.Arguments["execute"] != true {
				http.Error(w, "wrong call", http.StatusBadRequest)
				return
			}
			sawContextPlan = true
			result, _ := json.Marshal(map[string]any{"content": []map[string]any{{
				"type": "text", "text": `{"items":[{"namespace":"user/demo","key":"decision","payload":{"summary":"current"}}],"manifest":{"items_returned":1},"rationale":"test"}`,
			}}, "isError": false})
			_ = json.NewEncoder(w).Encode(mcp.JSONRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
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
