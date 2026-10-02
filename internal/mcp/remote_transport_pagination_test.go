package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gmcpclient "github.com/hollis-labs/go-mcp/client"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func paginatedTransport(t *testing.T, list func(context.Context, string) (*sdkmcp.ListToolsResult, error)) *remoteTransport {
	t.Helper()
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "pagination-fixture", Version: "test"}, nil)
	srv.AddReceivingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method == "tools/list" {
				return list(ctx, req.GetParams().(*sdkmcp.ListToolsParams).Cursor)
			}
			return next(ctx, method, req)
		}
	})
	ts := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil))
	t.Cleanup(ts.Close)
	pool := gmcpclient.NewPool(gmcpclient.WithRetries(0))
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.Register("paged", gmcpclient.ServerConfig{Transport: gmcpclient.TransportHTTP, URL: ts.URL}); err != nil {
		t.Fatal(err)
	}
	return newRemoteTransport(pool, "paged", gmcpclient.TransportHTTP)
}

func toolPage(name, next string) *sdkmcp.ListToolsResult {
	return &sdkmcp.ListToolsResult{Tools: []*sdkmcp.Tool{{Name: name, InputSchema: map[string]any{"type": "object"}}}, NextCursor: next}
}

func TestRemoteTransportListTools_AllPages(t *testing.T) {
	var mu sync.Mutex
	var cursors []string
	tr := paginatedTransport(t, func(_ context.Context, cursor string) (*sdkmcp.ListToolsResult, error) {
		mu.Lock()
		defer mu.Unlock()
		cursors = append(cursors, cursor)
		start, end, next := 0, 1000, "remaining"
		if cursor == "remaining" {
			start, end, next = 1000, 1501, ""
		}
		page := &sdkmcp.ListToolsResult{NextCursor: next}
		for i := start; i < end; i++ {
			page.Tools = append(page.Tools, &sdkmcp.Tool{Name: fmt.Sprintf("tool_%04d", i), InputSchema: map[string]any{"type": "object"}, Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true}})
		}
		return page, nil
	})
	got, err := tr.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1501 || got[0].Name != "tool_0000" || got[1500].Name != "tool_1500" {
		t.Fatalf("listing truncated or out of order: %d tools", len(got))
	}
	if got[1500].Annotations["readOnlyHint"] != true {
		t.Fatal("last-page annotations lost")
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(cursors) != "[ remaining]" {
		t.Fatalf("cursors: %q", cursors)
	}
}

func TestRemoteTransportListTools_RestartsAfterLaterPageFailure(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	tr := paginatedTransport(t, func(_ context.Context, cursor string) (*sdkmcp.ListToolsResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if cursor == "" {
			attempts++
			return toolPage(fmt.Sprintf("first_attempt_%d", attempts), "next"), nil
		}
		if attempts == 1 {
			return nil, errors.New("page connection lost")
		}
		return toolPage("last", ""), nil
	})
	got, err := tr.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "first_attempt_2" || got[1].Name != "last" {
		t.Fatalf("failed session's page leaked into retry: %+v", got)
	}
}

func TestRemoteTransportListTools_LaterPageErrorsReturnNoPartialList(t *testing.T) {
	for _, mode := range []string{"error", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			tr := paginatedTransport(t, func(_ context.Context, cursor string) (*sdkmcp.ListToolsResult, error) {
				mu.Lock()
				defer mu.Unlock()
				requests++
				if requests > 4 {
					return nil, errors.New("fixture request limit exceeded")
				}
				if cursor == "" {
					return toolPage("first", "next"), nil
				}
				if mode == "cycle" {
					return toolPage("second", "next"), nil
				}
				return nil, errors.New("page unavailable")
			})
			got, err := tr.ListTools(context.Background())
			if err == nil || got != nil {
				t.Fatalf("partial success: %+v, %v", got, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if requests != 4 {
				t.Fatalf("want two bounded pages per attempt and one reconnect, got %d requests", requests)
			}
			if mode == "cycle" && !strings.Contains(err.Error(), "repeated tools/list cursor") {
				t.Fatal(err)
			}
		})
	}
}

func TestRemoteTransportListTools_CancellationDuringLaterPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	tr := paginatedTransport(t, func(ctx context.Context, cursor string) (*sdkmcp.ListToolsResult, error) {
		if cursor == "" {
			return toolPage("first", "next"), nil
		}
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, errors.New("fixture released")
		}
	})
	done := make(chan error, 1)
	go func() {
		got, err := tr.ListTools(ctx)
		if got != nil {
			err = fmt.Errorf("partial list on cancellation: %+v", got)
		}
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
