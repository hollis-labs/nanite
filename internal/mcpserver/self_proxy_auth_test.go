package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSelfToolProxyTokenStaysAtHostEndpoint(t *testing.T) {
	const token = "test-only-operator-token"
	t.Setenv("NANITE_AUTH_TOKEN", token)
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
	}))
	defer destination.Close()
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tools/call" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("proxy did not target the authenticated tool endpoint")
		}
		http.Redirect(w, r, destination.URL+"/unexpected", http.StatusTemporaryRedirect)
	}))
	defer host.Close()
	proxy := newSelfToolProxy(host.URL, "view", ScopeHarness)
	_, err := proxy.CallTool(t.Context(), "todo_create", nil)
	if err == nil || destinationCalls.Load() != 0 || strings.Contains(err.Error(), token) {
		t.Fatalf("redirect outcome=%v destination calls=%d", err, destinationCalls.Load())
	}
}

type forbiddenHostTransport struct{ calls atomic.Int32 }

func (t *forbiddenHostTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, fmt.Errorf("unexpected transport call")
}

func TestSelfToolProxyRefusesUnintendedTokenDestination(t *testing.T) {
	t.Setenv("NANITE_AUTH_TOKEN", "test-only-token")
	for _, endpoint := range []string{"https://example.invalid", "http://192.0.2.1", "http://localhost", "http://user:pass@127.0.0.1", "http://127.0.0.1/alternate", "http://127.0.0.1?query=1"} {
		t.Run(endpoint, func(t *testing.T) {
			proxy := newSelfToolProxy(endpoint, "view", ScopeHarness)
			transport := &forbiddenHostTransport{}
			proxy.client.Transport = transport
			if _, err := proxy.CallTool(context.Background(), "todo_create", nil); err == nil {
				t.Fatal("unintended destination accepted")
			}
			if transport.calls.Load() != 0 {
				t.Fatal("token-bearing request escaped validation")
			}
		})
	}
}
