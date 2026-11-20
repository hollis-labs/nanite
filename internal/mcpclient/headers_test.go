package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

func TestConcurrentTurnHeadersExcludeMCPConnectionRequests(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			type request struct{ method, rpc, profile, static, want string }
			var mu sync.Mutex
			var seen []request
			server := sdk.NewServer(&sdk.Implementation{Name: "headers", Version: "1"}, nil)
			type input struct {
				Profile string `json:"profile"`
			}
			sdk.AddTool(server, &sdk.Tool{Name: "echo"}, func(_ context.Context, _ *sdk.CallToolRequest, in input) (*sdk.CallToolResult, any, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: in.Profile}}}, nil, nil
			})
			var handler http.Handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
			if transport == "sse" {
				handler = sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return server }, nil)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var rpc struct {
					Method string `json:"method"`
					Params struct {
						Arguments input `json:"arguments"`
					} `json:"params"`
				}
				if r.Body != nil {
					body, _ := io.ReadAll(r.Body)
					_ = r.Body.Close()
					r.Body = io.NopCloser(bytes.NewReader(body))
					_ = json.Unmarshal(body, &rpc)
				}
				mu.Lock()
				seen = append(seen, request{r.Method, rpc.Method, r.Header.Get("X-Profile"), r.Header.Get("X-Static"), rpc.Params.Arguments.Profile})
				mu.Unlock()
				handler.ServeHTTP(w, r)
			}))
			defer srv.Close()
			c := New(t.TempDir(), nil, redact.NewScope(), slog.Default(), nil)
			defer c.Close()
			cfg := agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"fixture": {Type: transport, URL: srv.URL, Headers: map[string]string{"x-static": "host"}}}}
			alpha, cancel := context.WithCancel(station.WithHeaders(t.Context(), map[string]string{"X-Profile": "alpha", "X-Static": "turn-alpha"}))
			defer cancel()
			beta := station.WithHeaders(t.Context(), map[string]string{"X-Profile": "beta", "X-Static": "turn-beta"})
			background := context.WithoutCancel(alpha)
			lease, warnings := c.Reconcile(alpha, cfg)
			if len(warnings) > 0 {
				t.Fatal(warnings)
			}
			defer lease.Close()
			offered, warnings := lease.Tools(alpha)
			if len(warnings) > 0 || len(offered) != 1 {
				t.Fatalf("discovery: %v", warnings)
			}
			call := func(ctx context.Context, value string) error {
				raw, _ := json.Marshal(input{Profile: value})
				_, err := offered[0].Handler(ctx, tools.Call{Input: raw})
				return err
			}
			errs := make(chan error, 2)
			go func() { errs <- call(alpha, "alpha") }()
			go func() { errs <- call(beta, "beta") }()
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			cancel() // Background work retains alpha after its parent turn ends.
			// Drop the stream, then invalidate the pooled session so the next call
			// establishes a new connection. Record both the old close and new GET.
			srv.CloseClientConnections()
			c.pool.Invalidate(lease.servers[0].key)
			ctx, stop := context.WithTimeout(background, 5*time.Second)
			defer stop()
			if err := call(ctx, "alpha"); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			gets, deletes, calls, lists := 0, 0, 0, 0
			for _, r := range seen {
				if r.static != "host" {
					t.Errorf("%s %s static=%q", r.method, r.rpc, r.static)
				}
				switch r.method {
				case http.MethodGet:
					gets++
				case http.MethodDelete:
					deletes++
				}
				if r.method != http.MethodPost && r.profile != "" {
					t.Errorf("connection %s leaked %q", r.method, r.profile)
				}
				switch r.rpc {
				case "tools/call":
					calls++
					if r.profile != r.want {
						t.Errorf("call profile=%q want %q", r.profile, r.want)
					}
				case "tools/list":
					lists++
					if r.profile != "alpha" {
						t.Errorf("list lost turn headers: %q", r.profile)
					}
				case "initialize":
					if r.profile != "" {
						t.Errorf("warm connection inherited %q", r.profile)
					}
				}
			}
			if gets < 2 || calls != 3 || lists == 0 || transport == "http" && deletes == 0 {
				t.Fatalf("requests: gets=%d deletes=%d calls=%d lists=%d", gets, deletes, calls, lists)
			}
		})
	}
}
