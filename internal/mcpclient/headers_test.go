package mcpclient

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/tools"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTurnHeadersOnReusedMCPConnection(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			server := testServer("ok")
			var handler http.Handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
			if transport == "sse" {
				handler = sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return server }, nil)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					mu.Lock()
					seen = append(seen, r.Header.Get("X-Profile"))
					mu.Unlock()
				}
				handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			c := New(t.TempDir(), nil, redact.NewScope(), slog.Default(), nil)
			defer c.Close()
			cfg := agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"fixture": {Type: transport, URL: srv.URL}}}
			for _, value := range []string{"alpha", "beta", ""} {
				ctx := t.Context()
				if value != "" {
					ctx = station.WithHeaders(ctx, map[string]string{"X-Profile": value})
				}
				lease, warnings := c.Reconcile(ctx, cfg)
				if len(warnings) > 0 {
					t.Fatal(warnings)
				}
				offered, warnings := lease.Tools(ctx)
				if len(warnings) > 0 || len(offered) != 1 {
					t.Fatalf("%v %v", offered, warnings)
				}
				mu.Lock()
				seen = nil
				mu.Unlock()
				if _, err := offered[0].Handler(ctx, tools.Call{Input: json.RawMessage(`{}`)}); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				for _, got := range seen {
					if got != value {
						t.Errorf("header=%q want %q", got, value)
					}
				}
				if len(seen) == 0 {
					t.Error("no request")
				}
				mu.Unlock()
				lease.Close()
			}
		})
	}
}
