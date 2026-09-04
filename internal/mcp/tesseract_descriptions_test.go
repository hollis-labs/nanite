package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTesseractToolDescriptionPinsV09Contract(t *testing.T) {
	desc, ok := TesseractToolDescriptions["tesseract_recall"]
	if !ok {
		t.Fatal("tesseract_recall description missing")
	}
	for _, want := range []string{
		"typed namespaces", "read prefix", "domains", "search_mode", "payload_mode",
		"manifest", "nullable", "zero or negative", "tesseract_get_revision", "tesseract_touch",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q", want)
		}
	}
	relations := TesseractToolRelations["tesseract_recall"]
	if len(relations.RelatedTools) == 0 || len(relations.RelatedSkills) == 0 {
		t.Fatalf("relations incomplete: %+v", relations)
	}
}

func TestTesseractRecallHTTPTransportRoundTrip(t *testing.T) {
	const token = "test-bearer"
	var sawList, sawCall bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "tools/list":
			sawList = true
			payload, _ := json.Marshal(map[string]any{"tools": []map[string]any{{
				"name": "tesseract_recall", "description": "recall",
				"inputSchema": map[string]any{"type": "object", "required": []string{"namespaces"}},
			}}})
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: payload})
		case "tools/call":
			sawCall = true
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			raw, _ := json.Marshal(req.Params)
			_ = json.Unmarshal(raw, &params)
			if params.Name != "tesseract_recall" || params.Arguments["payload_mode"] != "summary" {
				http.Error(w, "wrong call", http.StatusBadRequest)
				return
			}
			result, _ := json.Marshal(map[string]any{"content": []map[string]any{{
				"type": "text", "text": `{"results":[],"facets":{},"manifest":{"results_total":0,"results_returned":0,"bytes_returned":2,"tokens_estimate":1,"truncated":false,"truncation_reason":"","next_cursor":null}}`,
			}}, "isError": false})
			_ = json.NewEncoder(w).Encode(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	headers := map[string]string{"Authorization": "Bearer " + token}
	mgr := NewManager()
	t.Cleanup(mgr.Close)
	if err := mgr.AddHTTPServerWithHeaders("tesseract", srv.URL, headers, TierPluginHTTP); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	tools := mgr.GetAllToolsUnfiltered()
	if len(tools) != 1 || tools[0].Name != "tesseract_recall" || !sawList {
		t.Fatalf("registry tools=%v sawList=%v", tools, sawList)
	}
	result, err := mgr.ExecuteTool(context.Background(), "tesseract_recall", map[string]any{
		"namespaces": `["user/alice/memory"]`, "payload_mode": "summary",
	})
	if err != nil || !sawCall || !strings.Contains(result, `"results":[]`) {
		t.Fatalf("call result=%+v err=%v sawCall=%v", result, err, sawCall)
	}
}
