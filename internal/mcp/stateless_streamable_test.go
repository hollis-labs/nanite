package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// A stateless Streamable HTTP server is a legitimate deployment shape: every
// POST is independent, replies are plain application/json (never SSE), no
// Mcp-Session-Id is ever issued, GET returns 405 (no server-to-client stream)
// and DELETE returns 405 (nothing to terminate). These tests hand-roll such a
// server, because the official SDK handler would be stateful by default, and
// drive it through the same entry point the service uses
// (AddRemoteServerFromConfig, then discovery and a tool call).

type statelessRecord struct {
	method    string
	rpcMethod string
	auth      string
	hadSID    bool
}

type statelessServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []statelessRecord
	// wantAuth, when non-empty, makes every request without it a 401.
	wantAuth string
	// deleteStatus is what DELETE answers (405 or 404).
	deleteStatus int
	// discoverStatus, when non-zero, makes server/discover answer that bare
	// HTTP status instead of a JSON-RPC error.
	discoverStatus int
}

func (s *statelessServer) seen() []statelessRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]statelessRecord(nil), s.requests...)
}

func newStatelessServer(t *testing.T, wantAuth string, deleteStatus int) *statelessServer {
	t.Helper()
	s := &statelessServer{wantAuth: wantAuth, deleteStatus: deleteStatus}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)
		}
		s.mu.Lock()
		s.requests = append(s.requests, statelessRecord{
			method: r.Method, rpcMethod: req.Method,
			auth: r.Header.Get("Authorization"), hadSID: r.Header.Get("Mcp-Session-Id") != "",
		})
		s.mu.Unlock()

		if s.wantAuth != "" && r.Header.Get("Authorization") != s.wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Allow", "POST")
			http.Error(w, "no stream", http.StatusMethodNotAllowed)
			return
		case http.MethodDelete:
			http.Error(w, "stateless", s.deleteStatus)
			return
		case http.MethodPost:
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}

		reply := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}
		if req.Method == "server/discover" && s.discoverStatus != 0 {
			w.WriteHeader(s.discoverStatus)
			return
		}
		switch req.Method {
		case "initialize":
			reply(map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "stateless-fixture", "version": "0.0.1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			reply(map[string]any{"tools": []any{map[string]any{
				"name":        "tachyon_ping",
				"description": "ping",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"msg": map[string]any{"type": "string"}},
				},
			}}})
		case "tools/call":
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong"}}})
		default:
			// Unknown request (e.g. server/discover): a JSON-RPC error, as a
			// real server answers. Notifications (no id) get 202.
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}))
	t.Cleanup(s.Close)
	t.Cleanup(s.CloseClientConnections)
	return s
}

func countRPC(recs []statelessRecord, method string) int {
	n := 0
	for _, r := range recs {
		if r.method == http.MethodPost && r.rpcMethod == method {
			n++
		}
	}
	return n
}

func discoverAndCall(t *testing.T, m *Manager, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := m.DiscoverServerTools(ctx, name)
	if err != nil {
		t.Fatalf("DiscoverServerTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "tachyon_ping" || tools[0].InputSchema["type"] != "object" {
		t.Fatalf("tools = %+v, want one tachyon_ping with an object schema", tools)
	}
	out, err := m.ExecuteToolOnServer(ctx, name, "tachyon_ping", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("ExecuteToolOnServer: %v", err)
	}
	if !strings.Contains(out, "pong") {
		t.Fatalf("tool output = %q, want it to contain pong", out)
	}
}

func TestStatelessStreamable(t *testing.T) {
	cases := []struct {
		name         string
		auth         string
		headerJSON   string
		deleteStatus int
	}{
		{"pure_json_no_session_id", "", "", http.StatusMethodNotAllowed},
		{"static_authorization_header", "Bearer test-dummy", `{"Authorization":"Bearer test-dummy"}`, http.StatusMethodNotAllowed},
		{"delete_404", "", "", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newStatelessServer(t, tc.auth, tc.deleteStatus)
			m := NewManager()
			if err := m.AddRemoteServerFromConfig("stateless", store.TransportStreamable, srv.URL+"/api/agent/v1/mcp", tc.headerJSON, TierThirdPartyHTTP); err != nil {
				t.Fatalf("AddRemoteServerFromConfig: %v", err)
			}
			discoverAndCall(t, m, "stateless")
			m.Close() // session termination path: DELETE is optional, failure must not matter

			recs := srv.seen()
			t.Logf("requests: %+v", recs)
			if n := countRPC(recs, "initialize"); n != 1 {
				t.Errorf("initialize POSTs = %d, want 1 (one handshake reused across list and call)", n)
			}
			if n := countRPC(recs, "tools/call"); n != 1 {
				t.Errorf("tools/call POSTs = %d, want exactly 1 (no retry of a call that reached the server)", n)
			}
			for _, r := range recs {
				if r.hadSID {
					t.Errorf("client sent Mcp-Session-Id though the server never issued one: %+v", r)
				}
				if tc.auth != "" && r.auth != tc.auth {
					t.Errorf("request %+v missing the static Authorization header", r)
				}
				if r.method == http.MethodDelete {
					t.Errorf("client sent DELETE with no session to terminate: %+v", r)
				}
			}
			if w := m.GetDiscoveryWarnings(); len(w) != 0 {
				t.Errorf("discovery warnings = %+v, want none", w)
			}
		})
	}
}

// GET is how a stateful client opens the optional server-to-client stream.
// A 405 there is the spec's way of saying "none"; it must not break POSTs.
func TestStatelessStreamable_GET405IsTolerated(t *testing.T) {
	srv := newStatelessServer(t, "", http.StatusMethodNotAllowed)
	m := NewManager()
	defer m.Close()
	if err := m.AddRemoteServerFromConfig("stateless", store.TransportStreamable, srv.URL+"/mcp", "", TierThirdPartyHTTP); err != nil {
		t.Fatal(err)
	}
	discoverAndCall(t, m, "stateless")
	for _, r := range srv.seen() {
		if r.method == http.MethodGet {
			t.Logf("client issued GET (answered 405) and carried on")
		}
	}
}

// A missing credential must fail loudly rather than silently produce no tools.
func TestStatelessStreamable_WrongAuthFails(t *testing.T) {
	srv := newStatelessServer(t, "Bearer test-dummy", http.StatusMethodNotAllowed)
	m := NewManager()
	defer m.Close()
	if err := m.AddRemoteServerFromConfig("stateless", store.TransportStreamable, srv.URL+"/mcp", "", TierThirdPartyHTTP); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.DiscoverServerTools(ctx, "stateless"); err == nil {
		t.Fatal("discovery succeeded against a server that rejects the missing Authorization header")
	}
}

// The client probes server/discover before initialize. A stateless server that
// does not know it must still answer the probe. Pins what the client does with
// each way of declining so a go-mcp upgrade that changes it is noticed.
func TestStatelessStreamable_ServerDiscoverProbeDeclined(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := newStatelessServer(t, "", http.StatusMethodNotAllowed)
			srv.discoverStatus = status
			m := NewManager()
			defer m.Close()
			if err := m.AddRemoteServerFromConfig("stateless", store.TransportStreamable, srv.URL+"/mcp", "", TierThirdPartyHTTP); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := m.DiscoverServerTools(ctx, "stateless")
			// A bare 202 (body-less "accepted") is the one answer that breaks
			// the handshake: it is read as a response with no content type.
			// A 4xx, like a JSON-RPC method-not-found, falls back to initialize.
			if wantErr := status == http.StatusAccepted; (err != nil) != wantErr {
				t.Fatalf("bare HTTP %d for server/discover: err=%v, want error=%v", status, err, wantErr)
			}
		})
	}
}
