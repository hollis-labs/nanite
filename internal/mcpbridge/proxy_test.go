package mcpbridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

func TestProxyPreservesOneHostCallerBindingAndRefusesRetarget(t *testing.T) {
	credentials, _, _ := fixtureCredentials(t, 1)
	token := issueFixture(t, credentials, "one")
	var calls atomic.Int32
	owner := &ownerFixture{catalog: func(context.Context, VerifiedCaller) (Catalog, error) {
		return Catalog{Version: 1, Revision: "catalog-1", Tools: []ToolDefinition{{Tool: mcp.Tool{Name: "reviewed", InputSchema: map[string]any{"type": "object"}, Annotations: map[string]any{"idempotentHint": false}}, Effect: "write", Binding: "binding-1"}}}, nil
	}, call: func(ctx context.Context, caller VerifiedCaller, request CallRequest) (*mcp.ToolResult, error) {
		calls.Add(1)
		if _, err := RevalidateAuthority(ctx); err != nil {
			return nil, err
		}
		if caller.SessionID != "session-one" {
			t.Fatal("proxy changed verified session")
		}
		if request.Binding != "binding-1" {
			return nil, subprocess.ErrStaleBinding
		}
		return &mcp.ToolResult{IsError: true, Content: []mcp.ToolContent{{Type: "text", Text: "reviewed refusal"}}}, nil
	}}
	server := httptest.NewServer(fixtureHandler(t, credentials, owner))
	defer server.Close()
	proxy, err := NewProxy(server.URL, token, TransportLimits{MaxRequestBytes: 1024, MaxResponseBytes: 2048, MaxCallDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	if strings.Contains(fmt.Sprintf("%v %#v", proxy, proxy), token) {
		t.Fatal("proxy formatting exposed its bearer")
	}
	catalog, err := proxy.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Tools[0].Annotations["idempotentHint"] != false {
		t.Fatal("proxy lost explicit false annotation")
	}
	call := CallRequest{Version: 1, RequestID: "one", Name: "reviewed", Binding: "binding-1", Arguments: map[string]any{}, OperationKey: "receipt-1"}
	result, err := proxy.Call(context.Background(), call)
	if err != nil || !result.IsError {
		t.Fatal("proxy changed tool-level error")
	}
	call.Binding = "stale-binding"
	if _, err := proxy.Call(context.Background(), call); !errors.Is(err, subprocess.ErrStaleBinding) {
		t.Fatalf("stale binding was retargeted: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("proxy retried or retargeted a mutation")
	}
}

func TestProxyCredentialCannotFollowRedirectOrUnintendedDestination(t *testing.T) {
	credentials, _, _ := fixtureCredentials(t, 1)
	token := issueFixture(t, credentials, "one")
	limits := TransportLimits{MaxRequestBytes: 1024, MaxResponseBytes: 128, MaxCallDuration: time.Second}
	for _, endpoint := range []string{"http://example.com:80", "http://localhost:80", "http://user@127.0.0.1:80", "http://127.0.0.1:80/other", "http://127.0.0.1:80/?query=yes", "http://127.0.0.1:80/?", "http://127.0.0.1:80/#fragment", "http://127.0.0.1:80/#"} {
		if _, err := NewProxy(endpoint, token, limits); err == nil {
			t.Fatal("unintended credential destination accepted")
		}
	}
	var escaped atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { escaped.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	proxy, err := NewProxy(redirect.URL, token, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	if _, listErr := proxy.List(context.Background()); listErr == nil {
		t.Fatal("redirect accepted as an authoritative catalog")
	}
	if escaped.Load() != 0 {
		t.Fatal("credential followed a redirect")
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 256))) }))
	defer large.Close()
	bounded, err := NewProxy(large.URL, token, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.Close()
	if _, err := bounded.List(context.Background()); err == nil {
		t.Fatal("oversized response accepted")
	}
}
