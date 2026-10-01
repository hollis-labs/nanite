package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	toolresult "github.com/hollis-labs/go-toolresult"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/truncate"
	"github.com/hollis-labs/nanite/pkg/models"
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
	page, isErr := a.Services.ResultCache.Results.HandleFetch(context.Background(), "sess-a", map[string]any{"id": m[1], "length": float64(64)}, budget)
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
	view, err := a.Services.ResultCache.Results.Present(context.Background(), "sess-a", toolresult.Meta{CallID: "c1", Tool: "todo_list"}, body, 1000)
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

// D-35 on the CLI path: dispatched calls are persisted redacted; failures and
// sessionless calls are not, and persistence never affects the response.
func TestHandleSelfToolCall_PersistsRedactedArguments(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))
	mkSession(t, s, "sess-a", "")

	rec := postToolCall(t, a, map[string]any{"session_id": "sess-a", "name": "whoami",
		"args": map[string]any{"note": "audit me", "api_key": "sk-CLI-PATH-SECRET", "command": "run --token abc123def456"}})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got, err := a.Services.ResultCache.ListArguments("sess-a")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListArguments = %+v, %v", got, err)
	}
	if got[0].ToolName != "whoami" || !strings.Contains(got[0].Body, "audit me") {
		t.Errorf("record = %+v", got[0])
	}
	for _, leak := range []string{"sk-CLI-PATH-SECRET", "abc123def456"} {
		if strings.Contains(got[0].Body, leak) {
			t.Errorf("secret %q persisted: %s", leak, got[0].Body)
		}
	}

	// Cache-navigation calls are recorded as well.
	postToolCall(t, a, map[string]any{"session_id": "sess-a", "name": "fetch_tool_result", "args": map[string]any{"id": "nope"}})
	if got, _ = a.Services.ResultCache.ListArguments("sess-a"); len(got) != 2 {
		t.Errorf("cache-navigation call not recorded: %d rows", len(got))
	}

	// No session: nothing to attribute to, nothing stored, call still works.
	if rec := postToolCall(t, a, map[string]any{"name": "whoami", "args": map[string]any{"a": 1}}); rec.Code != 200 {
		t.Errorf("sessionless status %d", rec.Code)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM tool_call_arguments`).Scan(&n)
	if n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}

	// A persistence failure never fails the call.
	if _, err := s.DB.Exec(`DROP TABLE tool_call_arguments`); err != nil {
		t.Fatal(err)
	}
	if rec := postToolCall(t, a, map[string]any{"session_id": "sess-a", "name": "whoami"}); rec.Code != 200 {
		t.Errorf("status %d after persist failure", rec.Code)
	}
}

// What a CLI-launched session's tool-result budget is sized against, in order:
// a real model on the session, then the operator's harness.cli_models entry for
// that kind of CLI, then the floor. sessions.model for these sessions holds the
// wrapper's pseudo-model (observed in the dev database as "claude-cli" or
// empty), which is never a real model.
func TestCLISessionSizingModelOrder(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	if a.Services.AppConfig == nil {
		a.Services.AppConfig = config.DefaultAppConfig()
	}
	floor := truncate.BudgetForModel("")
	for _, m := range []string{"claude-cli", "bootprofile:claude-smoke", ""} {
		if got := truncate.BudgetForModel(m); got != floor {
			t.Fatalf("BudgetForModel(%q) = %d, want the floor %d", m, got, floor)
		}
	}
	mk := func(id, provider, model string) {
		t.Helper()
		if err := s.CreateSession(context.Background(), &store.Session{ID: id, Provider: provider, Model: model, Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("cli-claude", "pty", "claude-cli")  // the observed shape
	mk("cli-pty-claude", "pty-claude", "") // observed: empty model
	mk("cli-codex", "pty-codex", "codex-cli")
	mk("cli-gemini", "pty-gemini", "gemini-cli")  // a kind the operator did not declare
	mk("cli-real", "pty-claude", "claude-opus-5") // a session that does name a real model
	mk("cli-legacy", "bootprofile:claude-smoke", "bootprofile:claude-smoke")
	mk("api-real", "anthropic", "claude-opus-5")
	mk("api-unknown", "anthropic", "some-future-model") // API sessions are unchanged
	mk("api-empty", "", "")

	// Nothing declared: every CLI session is sized at the floor.
	for _, id := range []string{"cli-claude", "cli-pty-claude", "cli-codex", "cli-gemini"} {
		if got := a.sessionModel(context.Background(), id); got != "" {
			t.Errorf("no config, %s: sizing model %q, want \"\" (floor)", id, got)
		}
	}
	// A legacy encoded provider is not a CLI provider; its model string is not a
	// real model, so it is sized at the floor whatever is declared.
	if got := truncate.BudgetForModel(a.sessionModel(context.Background(), "cli-legacy")); got != floor {
		t.Errorf("legacy encoded session budget = %d, want the floor %d", got, floor)
	}
	// A CLI session that names a real model uses it, config or not.
	if got := a.sessionModel(context.Background(), "cli-real"); got != "claude-opus-5" {
		t.Errorf("cli-real = %q", got)
	}

	a.Services.AppConfig.Harness.CLIModels = map[string]string{"claude": "claude-opus-5", "codex": "claude-sonnet-5"}
	for id, want := range map[string]string{
		"cli-claude": "claude-opus-5", "cli-pty-claude": "claude-opus-5", // pty and pty-claude are the same kind
		"cli-codex":  "claude-sonnet-5",
		"cli-gemini": "", // undeclared kind: floor
		"cli-real":   "claude-opus-5",
	} {
		if got := a.sessionModel(context.Background(), id); got != want {
			t.Errorf("declared, %s: sizing model %q, want %q", id, got, want)
		}
	}
	// A declared name the registry does not know sizes at the floor, and says so
	// once; the same name resolves once the catalog has synced it.
	a.Services.AppConfig.Harness.CLIModels = map[string]string{"claude": "future-model-not-yet-in-any-catalog"}
	if got := a.sessionModel(context.Background(), "cli-claude"); got != "" {
		t.Errorf("unresolvable declared model: sizing model %q, want the floor", got)
	}
	models.SyncFromCatalog(models.CatalogInput{ContextWindows: map[string]int{"future-model-not-yet-in-any-catalog": 1_000_000}})
	t.Cleanup(func() { models.SyncFromCatalog(models.CatalogInput{}) })
	if got := a.sessionModel(context.Background(), "cli-claude"); got != "future-model-not-yet-in-any-catalog" {
		t.Errorf("after the catalog synced it: %q", got)
	}
	a.Services.AppConfig.Harness.CLIModels = map[string]string{"claude": "claude-opus-5", "codex": "claude-sonnet-5"}
	// API sessions are exactly as before, whatever is declared.
	for id, want := range map[string]string{"api-real": "claude-opus-5", "api-unknown": "some-future-model", "api-empty": ""} {
		if got := a.sessionModel(context.Background(), id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	// And the effect the declaration exists for: a 1M-window model's budget
	// instead of the floor.
	if truncate.BudgetForModel("claude-opus-5") <= floor {
		t.Skip("registry gives claude-opus-5 no larger budget than the floor here")
	}
	big := strings.Repeat("x\n", 7_500) // 15 KB: over the floor, under the scaled budget
	req := selfToolCallRequest{SessionID: "cli-claude", Name: "todo_list", CacheRetrieval: true}
	res := a.presentSelfToolResult(context.Background(), req, textResult(big))
	if res.Content[0].Text != big {
		t.Errorf("declared model did not scale the budget: result was cut to %d bytes", len(res.Content[0].Text))
	}
	a.Services.AppConfig.Harness.CLIModels = nil
	res = a.presentSelfToolResult(context.Background(), req, textResult(big))
	if res.Content[0].Text == big {
		t.Error("with nothing declared the same result must be cut at the floor")
	}
}

// CW-20260929-0021: the stored tool_call_id is unique per call, not per tool.
func TestHandleSelfToolCall_ToolCallIDIsPerCall(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))
	mkSession(t, s, "sess-a", "")

	for i := 0; i < 3; i++ {
		if rec := postToolCall(t, a, map[string]any{"session_id": "sess-a", "name": "whoami"}); rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
	}
	got, err := a.Services.ResultCache.ListArguments("sess-a")
	if err != nil || len(got) != 3 {
		t.Fatalf("ListArguments = %d rows, %v", len(got), err)
	}
	shape := regexp.MustCompile(`^self-tool:whoami:[0-9A-Z]{26}$`)
	seen := map[string]bool{}
	for _, r := range got {
		if !shape.MatchString(r.ToolCallID) {
			t.Errorf("tool_call_id = %q, want self-tool:whoami:<ulid>", r.ToolCallID)
		}
		if seen[r.ToolCallID] {
			t.Errorf("tool_call_id %q repeated across calls", r.ToolCallID)
		}
		seen[r.ToolCallID] = true
	}
}

// A caller's own call id is carried when it is well formed, and replaced when
// it is not.
func TestHandleSelfToolCall_CallerCallID(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	a.SetSelfTools(selftools.NewSelfToolsTransport(s))
	mkSession(t, s, "sess-a", "")

	postToolCall(t, a, map[string]any{"session_id": "sess-a", "name": "whoami", "call_id": "toolu_01AbC-9.x:y"})
	for _, bad := range []string{"has space", "semi;colon", strings.Repeat("a", maxCallIDLen+1), "new\nline"} {
		postToolCall(t, a, map[string]any{"session_id": "sess-a", "name": "whoami", "call_id": bad})
	}
	got, err := a.Services.ResultCache.ListArguments("sess-a")
	if err != nil || len(got) != 5 {
		t.Fatalf("ListArguments = %d rows, %v", len(got), err)
	}
	var carried int
	for _, r := range got {
		if r.ToolCallID == "self-tool:whoami:toolu_01AbC-9.x:y" {
			carried++
			continue
		}
		if !regexp.MustCompile(`^self-tool:whoami:[0-9A-Z]{26}$`).MatchString(r.ToolCallID) {
			t.Errorf("malformed call_id was stored as %q", r.ToolCallID)
		}
	}
	if carried != 1 {
		t.Errorf("well-formed call_id carried %d times, want 1", carried)
	}
}

// One call's argument row and its cached-result row carry the same id, so the
// two can be matched.
func TestSelfToolCall_ArgumentAndResultRowsShareID(t *testing.T) {
	a, s := newToolCallTestAPI(t)
	mkSession(t, s, "sess-a", "")
	req := cacheReq("sess-a")
	req.callID = selfToolCallID("")
	req.Args = map[string]any{"q": "x"}

	a.persistSelfToolArguments(req)
	a.presentSelfToolResult(context.Background(), req, textResult(strings.Repeat("row\n", truncate.BudgetForModel(""))))

	var argID, resID string
	if err := s.DB.QueryRow(`SELECT tool_call_id FROM tool_call_arguments WHERE session_id = 'sess-a'`).Scan(&argID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT tool_call_id FROM tool_result_cache WHERE session_id = 'sess-a'`).Scan(&resID); err != nil {
		t.Fatal(err)
	}
	if argID != resID || argID != "self-tool:todo_list:"+req.callID {
		t.Errorf("argument row %q, result row %q; want both self-tool:todo_list:%s", argID, resID, req.callID)
	}
}

// An unresolvable declared model warns once, naming the key and value.
func TestUnresolvedCLIModelWarnsOnce(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	unresolvedCLIModelWarned.Delete("claude=warn-once-model")

	a := &API{Services: &service.Container{AppConfig: &config.TunablesConfig{Harness: config.HarnessConfig{CLIModels: map[string]string{"claude": "warn-once-model"}}}}}
	for i := 0; i < 3; i++ {
		if got := a.cliSizingModel("pty-claude", "claude-cli"); got != "" {
			t.Fatalf("sizing model = %q, want the floor", got)
		}
	}
	out := buf.String()
	if strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, "harness.cli_models.claude") || !strings.Contains(out, "warn-once-model") || !strings.Contains(out, "level=WARN") {
		t.Errorf("want exactly one warning naming key and value, got:\n%s", out)
	}
}
