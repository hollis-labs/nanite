package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// newToolCallTestAPI builds an API plus the store backing it, so the test
// can wire a SelfToolsTransport against the same DB.
func newToolCallTestAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:     s,
		Providers: provider.NewRegistry(),
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	return New(svc), s
}

func postToolCall(t *testing.T, a *API, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tools/call", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	a.handleSelfToolCall(rec, req)
	return rec
}

// TestHandleSelfToolCall_Unavailable pins that the endpoint 503s when no
// self-tools transport has been wired (SetSelfTools never called).
func TestHandleSelfToolCall_Unavailable(t *testing.T) {
	a, _ := newToolCallTestAPI(t)
	rec := postToolCall(t, a, map[string]any{"name": "todo_create"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestHandleSelfToolCall_MissingName pins the 400 for a nameless request.
func TestHandleSelfToolCall_MissingName(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(mcp.NewSelfToolsTransport(s))
	rec := postToolCall(t, a, map[string]any{"args": map[string]any{}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestHandleSelfToolCall_DispatchesUnknownTool pins that a well-formed
// request reaches SelfToolsTransport.CallTool: an unknown tool name comes
// back as a 200 with a tool-level error result (IsError), NOT a transport
// failure. This proves the routing without depending on any wired service.
func TestHandleSelfToolCall_DispatchesUnknownTool(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(mcp.NewSelfToolsTransport(s))

	rec := postToolCall(t, a, map[string]any{"name": "definitely_not_a_real_tool"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (dispatch succeeded, tool-level error)", rec.Code)
	}
	var res mcp.ToolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, rec.Body.String())
	}
	if !res.IsError {
		t.Errorf("unknown tool should yield IsError=true, got %#v", res)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "unknown tool") {
		t.Errorf("result should mention 'unknown tool', got %#v", res.Content)
	}
}

// TestHandleSelfToolCall_StampsSessionAndDispatches pins the end-to-end
// path: the endpoint stamps the request's session_id onto the dispatch
// context, and a session-scoped todo_create (which needs both a wired
// TodoStore and a session in context) succeeds through it.
func TestHandleSelfToolCall_StampsSessionAndDispatches(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	st := mcp.NewSelfToolsTransport(s)
	st.TodoStore = s // *store.Store satisfies the TodoStore interface
	a.SetSelfTools(st)

	rec := postToolCall(t, a, map[string]any{
		"session_id": "sess-tool-call-test",
		"name":       "todo_create",
		"args":       map[string]any{"title": "smoke item"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", rec.Code, rec.Body.String())
	}
	var res mcp.ToolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, rec.Body.String())
	}
	if res.IsError {
		t.Errorf("todo_create should succeed with TodoStore wired + session stamped, got %#v", res.Content)
	}
}
