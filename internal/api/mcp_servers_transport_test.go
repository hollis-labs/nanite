package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// The API is the only way a transport_type reaches the database, so what it
// accepts decides which transports are reachable at all. "streamable" was
// added by the SSE work: rejecting it here would leave every ContextForge
// /mcp URL unconfigurable.
func TestCreateMCPServerAcceptsEveryRealTransport(t *testing.T) {
	for _, transport := range []string{store.TransportStdio, store.TransportSSE, store.TransportStreamable} {
		t.Run(transport, func(t *testing.T) {
			_, mux := newTestAPI(t)

			body := map[string]any{
				"name":           "probe-" + transport,
				"transport_type": transport,
				"command":        "/bin/true",
				"url":            "http://gateway.invalid/servers/x/" + transport,
			}
			rec := postJSON(t, mux, "/api/mcp-servers", body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST /api/mcp-servers = %d, want 201: %s", rec.Code, rec.Body.String())
			}

			var got store.MCPServerConfig
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.TransportType != transport {
				t.Fatalf("stored transport_type = %q, want %q", got.TransportType, transport)
			}
		})
	}
}

func TestCreateMCPServerRejectsAnUnknownTransport(t *testing.T) {
	_, mux := newTestAPI(t)

	rec := postJSON(t, mux, "/api/mcp-servers", map[string]any{
		"name":           "probe",
		"transport_type": "websocket",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with an unknown transport = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	// The message has to name every accepted value: "sse" and "streamable"
	// are easy to pick wrongly, and a 400 that lists neither leaves the
	// operator guessing which one their URL speaks.
	for _, want := range []string{"stdio", "sse", "streamable"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("400 body does not mention %q: %s", want, rec.Body.String())
		}
	}
}

func postJSON(t *testing.T, mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
