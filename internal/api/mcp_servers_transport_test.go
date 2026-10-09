package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// API writes accept only operational transports; retained legacy rows are
// independently refused by runtime registration as well.
func TestCreateMCPServerAcceptsEveryRealTransport(t *testing.T) {
	for _, transport := range []string{store.TransportStdio, store.TransportStreamable} {
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

func TestMCPServerLegacyRefusalHasNoDurableEffects(t *testing.T) {
	a, mux := newTestAPI(t)
	for _, cfg := range []map[string]any{
		{"name": "legacy", "transport_type": "sse", "url": "http://gateway.invalid/sse"},
		{"name": "legacy", "transport_type": "streamable", "url": "http://gateway.invalid/servers/x/sse"},
	} {
		rec := postJSON(t, mux, "/api/mcp-servers", cfg)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "verify the server's Streamable HTTP endpoint") {
			t.Fatalf("legacy refusal: %d %s", rec.Code, rec.Body.String())
		}
		row, err := a.store.GetMCPServer(context.Background(), "legacy")
		if err != nil || row != nil {
			t.Fatalf("refusal persisted row: %+v %v", row, err)
		}
	}
	imported := mcpDo(mux, "POST", "/api/mcp-servers/import", `{"mcpServers":{"a-valid":{"command":"true"},"z-legacy":{"type":"sse","url":"http://gateway.invalid/sse"}}}`)
	if imported.Code != http.StatusBadRequest || !strings.Contains(imported.Body.String(), "Streamable HTTP endpoint") {
		t.Fatalf("legacy import: %d %s", imported.Code, imported.Body.String())
	}
	partial, partialErr := a.store.GetMCPServer(context.Background(), "a-valid")
	if partialErr != nil || partial != nil {
		t.Fatalf("rejected import partially persisted: %+v %v", partial, partialErr)
	}
	row := &store.MCPServerConfig{Name: "retained", TransportType: store.TransportSSE, URL: "http://gateway.invalid/sse", Headers: `{"Authorization":"Bearer private"}`, Enabled: true}
	if err := a.store.CreateMCPServer(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	rec := mcpDo(mux, "PUT", "/api/mcp-servers/retained", `{"enabled":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reactivation: %d %s", rec.Code, rec.Body.String())
	}
	stored, err := a.store.GetMCPServer(context.Background(), "retained")
	if err != nil || stored.URL != row.URL || stored.Headers != row.Headers {
		t.Fatalf("refusal mutated row: %+v %v", stored, err)
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
	// The message names the supported choices; legacy SSE has its own
	// explicit migration refusal rather than masquerading as a supported kind.
	for _, want := range []string{"stdio", "streamable"} {
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
