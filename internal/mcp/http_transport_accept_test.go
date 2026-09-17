package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Some MCP gateways answer a request that does not explicitly accept JSON
// with `406 Not Acceptable: Client must accept application/json`. Go sends no
// Accept header unless one is set, so before this header was added every
// tools/list against a gateway-fronted server failed discovery — the server was
// registered, authenticated and reachable, and published no tools.
func TestHTTPTransport_SendsAcceptJSON(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer srv.Close()

	if _, err := NewHTTPTransport(srv.URL).ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	// Exactly application/json, not the spec's
	// "application/json, text/event-stream": this transport has no SSE
	// decoder, so offering an event stream invites a response it cannot read.
	if got != "application/json" {
		t.Fatalf("Accept = %q, want %q", got, "application/json")
	}
}

// A caller-supplied Accept still wins, matching how Content-Type behaves: the
// static header map is applied after the defaults.
func TestHTTPTransport_CallerAcceptOverridesDefault(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer srv.Close()

	tr := NewHTTPTransportWithHeaders(srv.URL, map[string]string{"Accept": "application/json, text/event-stream"})
	if _, err := tr.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if got != "application/json, text/event-stream" {
		t.Fatalf("Accept = %q, want the caller's value", got)
	}
}
