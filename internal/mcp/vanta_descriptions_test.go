package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestVantaToolDescriptions_MemoryRecallShape ensures the Bucket-2 chat-surface
// description for memory_recall covers the architectural requirements so a
// future authoring drift is caught at test time.
//
// CW-20260501-0005 sub-ticket 2.
func TestVantaToolDescriptions_MemoryRecallShape(t *testing.T) {
	desc, ok := VantaToolDescriptions["memory_recall"]
	if !ok {
		t.Fatal("memory_recall description missing from VantaToolDescriptions")
	}

	// Must include reactive-framing guard and the c114 anti-pattern reference
	// (the description's load-bearing contribution vs. Vanta's generic copy).
	wantSnippets := []string{
		"Reactively",
		"do NOT",
		"c114",
		"Contract",
		"Golden example",
		"Cross-references",
	}
	for _, want := range wantSnippets {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q: chat-surface contract requires it", want)
		}
	}
}

func TestVantaToolRelations_MemoryRecall(t *testing.T) {
	rel, ok := VantaToolRelations["memory_recall"]
	if !ok {
		t.Fatal("memory_recall relations missing from VantaToolRelations")
	}
	if len(rel.RelatedTools) == 0 {
		t.Error("RelatedTools must not be empty — memory_recall is part of the Vanta read/write cluster")
	}
	if len(rel.RelatedSkills) == 0 {
		t.Error("RelatedSkills must not be empty — capture-to-vanta is the write-side discipline that pairs with recall")
	}
}

// TestVantaMemoryRecallRoundTrip drives a fake Vanta MCP HTTP server through
// the full discovery + tools/call path so the chat-surface integration is
// known to work end-to-end. The test asserts:
//
//  1. AddHTTPServerWithHeaders attaches the Authorization header to every
//     JSON-RPC request (auth flows from cfg → header → wire).
//  2. ListTools returns the tool definition published by the upstream server.
//  3. CallTool returns a parseable ToolResult envelope.
//
// The shape mirrors how `registerVantaServer` wires the real Vanta server into
// the MCP manager. CW-20260501-0005 sub-ticket 2.
func TestVantaMemoryRecallRoundTrip(t *testing.T) {
	const wantToken = "test-bearer-xyz"
	const wantQuery = "card_show envelope schema"

	var (
		sawListAuth bool
		sawCallAuth bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth header propagation check — load-bearing for the chat surface
		// because Vanta will reject anonymous calls.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+wantToken {
			http.Error(w, "missing/invalid auth", http.StatusUnauthorized)
			return
		}

		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "tools/list":
			sawListAuth = true
			payload := map[string]any{
				"tools": []map[string]any{
					{
						"name":        "memory_recall",
						"description": "Recall memories matching a natural-language query.",
						"inputSchema": map[string]any{
							"type":     "object",
							"required": []string{"query"},
							"properties": map[string]any{
								"query": map[string]any{"type": "string"},
								"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
						},
					},
					{
						"name":        "memory_write",
						"description": "Persist a memory entry.",
						"inputSchema": map[string]any{"type": "object"},
					},
				},
			}
			raw, _ := json.Marshal(payload)
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  raw,
			}
			_ = json.NewEncoder(w).Encode(resp)

		case "tools/call":
			sawCallAuth = true
			// Decode params + spot-check the query made it through the
			// transport intact (so we know argument forwarding works).
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if rawParams, err := json.Marshal(req.Params); err == nil {
				_ = json.Unmarshal(rawParams, &p)
			}
			if p.Name != "memory_recall" {
				http.Error(w, "unexpected tool name", http.StatusBadRequest)
				return
			}
			if got, _ := p.Arguments["query"].(string); got != wantQuery {
				http.Error(w, "query mismatch", http.StatusBadRequest)
				return
			}

			result := map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": `{"memories":[{"namespace":"user/test/memory","key":"k1","summary":"prior lesson","tags":["learning","tool:card_show"]}]}`,
					},
				},
				"isError": false,
			}
			raw, _ := json.Marshal(result)
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  raw,
			}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	mgr := NewManager()
	headers := map[string]string{"Authorization": "Bearer " + wantToken}
	if err := mgr.AddHTTPServerWithHeaders("vanta", srv.URL, headers, TierPluginHTTP); err != nil {
		t.Fatalf("AddHTTPServerWithHeaders: %v", err)
	}

	tools, err := mgr.DiscoverServerTools(context.Background(), "vanta")
	if err != nil {
		t.Fatalf("DiscoverServerTools: %v", err)
	}
	if !sawListAuth {
		t.Error("upstream did not see Authorization header on tools/list")
	}
	var foundRecall bool
	for _, tool := range tools {
		if tool.Name == "memory_recall" {
			foundRecall = true
			break
		}
	}
	if !foundRecall {
		t.Fatalf("memory_recall not in discovered tools: %v", tools)
	}

	// Round-trip a tools/call. We use the transport directly because
	// Manager.ExecuteTool requires DiscoverTools to have populated the
	// uniform-name index, and that path is exercised in other tests; here
	// we want the narrow "argument forwarding + auth header + result
	// envelope parses" guarantee for Vanta.
	tr := NewHTTPTransportWithHeaders(srv.URL, headers)
	res, err := tr.CallTool(context.Background(), "memory_recall", map[string]any{
		"query": wantQuery,
		"tags":  []string{"learning", "tool:card_show"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !sawCallAuth {
		t.Error("upstream did not see Authorization header on tools/call")
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("unexpected ToolResult content: %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "memories") {
		t.Errorf("expected text content to carry memories payload, got %q", res.Content[0].Text)
	}
	if res.IsError {
		t.Error("expected IsError=false")
	}
}

// TestVantaMemoryRecallRoundTrip_NoAuthRequired covers the optional-token path
// — when no Authorization header is configured, AddHTTPServer is used and the
// upstream sees no auth header. Mirrors registerVantaServer's behavior when
// cfg.Vanta.Token is empty.
func TestVantaMemoryRecallRoundTrip_NoAuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject if auth header is present — confirms the no-token path
		// genuinely sends no Authorization (we don't want to silently send
		// an empty bearer).
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected auth", http.StatusBadRequest)
			return
		}

		var req JSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"tools": []map[string]any{
				{"name": "memory_recall", "description": "anon", "inputSchema": map[string]any{"type": "object"}},
			},
		}
		raw, _ := json.Marshal(payload)
		_ = json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: raw})
	}))
	defer srv.Close()

	mgr := NewManager()
	if err := mgr.AddHTTPServer("vanta", srv.URL, TierPluginHTTP); err != nil {
		t.Fatalf("AddHTTPServer: %v", err)
	}
	tools, err := mgr.DiscoverServerTools(context.Background(), "vanta")
	if err != nil {
		t.Fatalf("DiscoverServerTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool on tools/list")
	}
}
