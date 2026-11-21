package mcpclient

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/warden"
	"github.com/hollis-labs/nanite/internal/warden/wardentest"
)

func TestWardenAuthAndCorrelationOnMCPHTTPRequests(t *testing.T) {
	var mu sync.Mutex
	ws := wardentest.NewFake(t, "parent-0").Grant("mcp-gateway").
		Tokens(func(n int, _ string) string { return fmt.Sprintf("mcp-tok-%d", n) })
	file := filepath.Join(t.TempDir(), "agent.cred")
	if err := tokenfile.Write(file, "parent-0"); err != nil {
		t.Fatal(err)
	}
	src, err := warden.New(warden.Config{URL: ws.URL, CredentialFile: file})
	if err != nil {
		t.Fatal(err)
	}
	var seen http.Header
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Clone()
		mu.Unlock()
	}))
	t.Cleanup(mcpSrv.Close)

	auth := &Auth{Principal: "agent:test", Warden: src, Servers: map[string]authored.MCPIdentity{
		"gateway": {Audience: "mcp-gateway", Scopes: []string{"mcp.profile.standard"}, Correlation: true}}}
	c := New(t.TempDir(), nil, redact.NewScope(), slog.Default(), auth)
	cfg, err := c.resolve(t.Context(), "gateway", agentdir.MCPServer{Type: "http", URL: mcpSrv.URL, Headers: map[string]string{"X-Team": "nanite"}})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(station.WithSessionID(context.Background(), "conv-1"), http.MethodPost, mcpSrv.URL, nil)
	resp, err := c.httpClient(cfg.Headers, 5).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if got := seen.Get("Authorization"); got != "Bearer mcp-tok-1" {
		t.Fatalf("Authorization %q", got)
	}
	if seen.Get(PrincipalHeader) != "agent:test" || seen.Get(ConversationHeader) != "conv-1" || seen.Get("X-Team") != "nanite" {
		t.Fatalf("headers %v", seen)
	}
	if seen.Get(authMarker) != "" {
		t.Fatal("the internal auth marker was sent")
	}

	// A server without an identity entry gets neither.
	plain, err := c.resolve(t.Context(), "other", agentdir.MCPServer{Type: "http", URL: mcpSrv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain.Headers[authMarker]; ok {
		t.Fatal("a server without identity got Warden auth")
	}
	// An explicit Authorization header and Warden auth conflict.
	if _, err := c.resolve(t.Context(), "gateway", agentdir.MCPServer{Type: "http", URL: mcpSrv.URL, Headers: map[string]string{"Authorization": "static"}}); err == nil {
		t.Fatal("an Authorization header alongside Warden auth was accepted")
	}
}
