package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/truncate"
)

func textResult(s string) *mcp.ToolResult {
	return &mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: s}}}
}

func mkSession(t *testing.T, s *store.Store, id, model string) {
	t.Helper()
	if err := s.CreateSession(context.Background(), &store.Session{ID: id, Model: model, Status: "active"}); err != nil {
		t.Fatal(err)
	}
}

func cacheReq(session string) selfToolCallRequest {
	return selfToolCallRequest{SessionID: session, Name: "todo_list", CacheRetrieval: true}
}

func TestPresentSelfToolResult_OverBudgetIsCachedWithPointer(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	mkSession(t, s, "sess-a", "")
	budget := truncate.BudgetForModel("")
	body := strings.Repeat("row of output\n", budget) // far over budget
	got := a.presentSelfToolResult(context.Background(), cacheReq("sess-a"), textResult(body))
	if len(got.Content) != 1 {
		t.Fatalf("content = %+v", got.Content)
	}
	text := got.Content[0].Text
	if len(text) >= len(body)/2 {
		t.Errorf("result not bounded: %d of %d bytes", len(text), len(body))
	}
	m := regexp.MustCompile(`tool_result://([0-9A-Z]{26})`).FindStringSubmatch(text)
	if m == nil || !strings.Contains(text, "fetch_tool_result") || !strings.Contains(text, "PARTIAL PREVIEW") {
		t.Fatalf("no recovery pointer in: %.400s", text)
	}
	// Retrieval through the same endpoint returns the original bytes.
	page, isErr := a.Services.ResultCache.FetchToolResult("sess-a", map[string]any{"id": m[1], "length": float64(64)}, budget)
	if isErr || !strings.Contains(page, "row of output") {
		t.Errorf("fetch = %v %q", isErr, page)
	}
}

func TestPresentSelfToolResult_PassThroughCases(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	mkSession(t, s, "sess-a", "")
	big := strings.Repeat("x", 200_000)

	cases := []struct {
		name string
		req  selfToolCallRequest
		res  *mcp.ToolResult
	}{
		{"under budget", cacheReq("sess-a"), textResult("small")},
		{"error result", cacheReq("sess-a"), &mcp.ToolResult{IsError: true, Content: []mcp.ToolContent{{Type: "text", Text: big}}}},
		{"caller cannot retrieve", selfToolCallRequest{SessionID: "sess-a", Name: "todo_list"}, textResult(big)},
		{"no session", selfToolCallRequest{Name: "todo_list", CacheRetrieval: true}, textResult(big)},
		{"discovery exempt", selfToolCallRequest{SessionID: "sess-a", Name: "tool_describe", CacheRetrieval: true}, textResult(big)},
		{"no text block", cacheReq("sess-a"), &mcp.ToolResult{Content: []mcp.ToolContent{{Type: "image"}}}},
	}
	for _, tc := range cases {
		got := a.presentSelfToolResult(context.Background(), tc.req, tc.res)
		if got != tc.res {
			t.Errorf("%s: result was replaced", tc.name)
		}
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM tool_result_cache`).Scan(&n); err != nil || n != 0 {
		t.Errorf("cache rows = %d (%v), want 0 for pass-through cases", n, err)
	}
}

func TestPresentSelfToolResult_MixedBlocksAndNilCache(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	mkSession(t, s, "sess-a", "")
	big := strings.Repeat("y", 50_000)
	got := a.presentSelfToolResult(context.Background(), cacheReq("sess-a"), &mcp.ToolResult{Content: []mcp.ToolContent{
		{Type: "text", Text: big}, {Type: "image", Text: "img"}, {Type: "text", Text: "tail"},
	}})
	if len(got.Content) != 2 || got.Content[0].Type != "text" || got.Content[1].Type != "image" {
		t.Fatalf("blocks = %+v", got.Content)
	}
	if strings.Contains(got.Content[0].Text, "tail") && !strings.Contains(got.Content[0].Text, "tool_result://") {
		t.Error("second text block lost without a pointer")
	}
	// A failing cache never fails the call: original comes back.
	if _, err := s.DB.Exec(`DROP TABLE tool_result_cache`); err != nil {
		t.Fatal(err)
	}
	res := textResult(big)
	if a.presentSelfToolResult(context.Background(), cacheReq("sess-a"), res) != res {
		t.Error("cache failure altered the result")
	}
}

func TestPresentSelfToolResult_BudgetFollowsSessionModel(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	small, large := "no-such-model-xyz", "claude-opus-5"
	if truncate.BudgetForModel(large) <= truncate.BudgetForModel(small) {
		t.Skipf("model catalog gives no larger budget for %s here", large)
	}
	mkSession(t, s, "sess-small", small)
	mkSession(t, s, "sess-large", large)
	body := strings.Repeat("z", (truncate.BudgetForModel(small)+truncate.BudgetForModel(large))/2)
	if got := a.presentSelfToolResult(context.Background(), cacheReq("sess-small"), textResult(body)); got.Content[0].Text == body {
		t.Error("small-window session was not bounded")
	}
	if got := a.presentSelfToolResult(context.Background(), cacheReq("sess-large"), textResult(body)); got.Content[0].Text != body {
		t.Error("large-window session was bounded below its model budget")
	}
}

// End to end over the handler: a huge self-tool result comes back bounded with
// a pointer, and fetch_tool_result / search_tool_result on the same endpoint
// recover it — scoped to the session.
func TestHandleSelfToolCall_CacheNavigationRoundTrip(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))
	mkSession(t, s, "sess-a", "")
	mkSession(t, s, "sess-b", "")
	body := strings.Repeat("filler line\n", 20_000) + "NEEDLE-42\n"
	view, err := a.Services.ResultCache.PresentResult("sess-a", "c1", "todo_list", body, 1000)
	if err != nil || !view.Cached {
		t.Fatalf("seed: %+v %v", view, err)
	}

	decode := func(body any) (mcp.ToolResult, int) {
		rec := postToolCall(t, a, body)
		var r mcp.ToolResult
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		return r, rec.Code
	}
	r, code := decode(map[string]any{"session_id": "sess-a", "name": "search_tool_result", "args": map[string]any{"id": view.CacheID, "pattern": "NEEDLE"}})
	if code != 200 || r.IsError || !strings.Contains(r.Content[0].Text, "NEEDLE-42") {
		t.Errorf("search = %d %+v", code, r)
	}
	r, _ = decode(map[string]any{"session_id": "sess-a", "name": "fetch_tool_result", "args": map[string]any{"id": view.CacheID}})
	if r.IsError || !strings.Contains(r.Content[0].Text, "filler line") {
		t.Errorf("fetch = %+v", r)
	}
	r, _ = decode(map[string]any{"session_id": "sess-b", "name": "fetch_tool_result", "args": map[string]any{"id": view.CacheID}})
	if !r.IsError {
		t.Errorf("another session read the cached result: %+v", r)
	}
	r, _ = decode(map[string]any{"name": "fetch_tool_result", "args": map[string]any{"id": view.CacheID}})
	if !r.IsError {
		t.Errorf("sessionless call read the cache: %+v", r)
	}
}
