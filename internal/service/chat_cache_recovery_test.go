package service

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

type recoverySource struct{ body string }

func (r recoverySource) ListTools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "portfolio_source", Description: "Read a portfolio source.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}}, nil
}

func TestChatCacheRecovery_CompleteResultBypassesLegacyLineLimit(t *testing.T) {
	svc := makeErrorHonestyService()
	svc.resultCache = tool.NewResultCache(nil, tool.ResultCacheConfig{})
	body := strings.Repeat("row\n", 300) + "FINAL ROW"
	tu := llmtypes.ToolUseBlock{ID: "source", Name: "portfolio_source", Input: map[string]any{}}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 32)
	blocks, _ := svc.postProcessToolResults(context.Background(),
		[]toolPlan{{tu: tu, status: toolPlanReady}},
		[]toolExecResult{{rawOutput: body, ref: chat.ToolCallRef{ID: tu.ID, Name: tu.Name}}},
		ls, ch, "session", "agent", "message", "")
	if len(blocks) != 1 || blocks[0].Content != body {
		t.Fatal("complete result that fits the byte budget was truncated by its line count")
	}
}

func (r recoverySource) CallTool(context.Context, string, map[string]any) (*mcp.ToolResult, error) {
	return &mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: r.body}}}, nil
}

type recoverySurface struct{ ToolService }

func (r recoverySurface) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return &ToolSelection{Tools: []llmtypes.ToolDefinition{
		{Name: "portfolio_source", Description: "Read a portfolio source.", InputSchema: map[string]any{"type": "object"}},
		{Name: "tool_describe", Description: "Describe a tool.", InputSchema: map[string]any{"type": "object"}},
	}}, nil
}

func TestChatCacheRecovery_SearchThenFetchHiddenEvidence(t *testing.T) {
	for _, source := range []string{"task", "python_stdout"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			target := "DEPLOYMENT VERIFIED and the corrected implementation is active"
			text := strings.Repeat("historical context\n", 1500) + target + "\n" + strings.Repeat("remaining source\n", 1500)
			pointer := "/stdout"
			data := map[string]any{"stdout": text, "stderr": "", "result": nil}
			if source == "task" {
				pointer = "/data/comments/2/content"
				data = map[string]any{"ok": true, "data": map[string]any{
					"id": "task-example", "title": "Repair tool discovery", "status": "review",
					"comments": []any{
						map[string]any{"content": "Original investigation; no code changed."},
						map[string]any{"content": "Implementation in progress."},
						map[string]any{"content": text},
					},
				}}
			}
			body, _ := json.MarshalIndent(data, "", "  ")
			searchArgs := map[string]any{"id": "pending", "json_pointer": pointer, "pattern": "DEPLOYMENT VERIFIED"}
			fetchArgs := map[string]any{"id": "pending", "json_pointer": pointer, "length": float64(100)}
			var f *characterizationFixture
			var cacheID string
			var callbackErr error
			steps := []characterizationProviderStep{
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "describe", Name: "tool_describe", Input: map[string]any{"name": "fetch_tool_result"}})},
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "source", Name: "portfolio_source", Input: map[string]any{}})},
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "search", Name: "search_tool_result", Input: searchArgs}), beforeReturn: func() {
					callbackErr = f.st.DB.QueryRow("SELECT id FROM tool_result_cache WHERE session_id = ? AND tool_call_id = 'source'", f.session).Scan(&cacheID)
					searchArgs["id"], fetchArgs["id"] = cacheID, cacheID
				}},
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "fetch", Name: "fetch_tool_result", Input: fetchArgs}), beforeReturn: func() {
					// The scripted model uses coordinates delivered in the previous
					// tool result, rather than calculating an offset from the fixture.
					request := f.provider.requests[len(f.provider.requests)-1]
					for _, message := range request.Messages {
						for _, block := range message.ContentBlocks {
							if block.ToolUseID == "search" {
								match := regexp.MustCompile(`match bytes (\d+)\.\.`).FindStringSubmatch(block.Content)
								if len(match) == 2 {
									n, _ := strconv.Atoi(match[1])
									fetchArgs["offset"] = float64(n)
								}
							}
						}
					}
				}},
				{events: doneEvents("Recovered the current disposition from the cached result.")},
			}
			f = newCharacterizationFixture(t, steps)
			f.svc.resultCache = tool.NewResultCache(f.st.DB, tool.ResultCacheConfig{})
			f.svc.inspector = inspector.NewService()
			agent := f.svc.agents.(*characterizationAgents).agent
			if err := f.st.CreateAgent(ctx, agent); err != nil {
				t.Fatal(err)
			}
			manager := mcp.NewManager()
			self := selftools.NewSelfToolsTransport(f.st)
			if err := manager.AddServer("self", self, mcp.TierBuiltin); err != nil {
				t.Fatal(err)
			}
			if err := manager.AddServer("source", recoverySource{string(body)}, mcp.TierBuiltin); err != nil {
				t.Fatal(err)
			}
			if err := manager.DiscoverTools(ctx); err != nil {
				t.Fatal(err)
			}
			client := toolclient.New(manager, f.st, nil)
			client.Builtins.RegisterBuiltins("result-cache", []llmtypes.ToolDefinition{toolclient.FetchToolResultMetaTool(), toolclient.SearchToolResultMetaTool()})
			self.Inventory = client
			// Source reads require grants. Cache reads deliberately have none:
			// they operate solely on results already owned by this session.
			for _, name := range []string{"portfolio_source", "tool_describe"} {
				id, err := f.st.UpsertKnownTool(ctx, name, "builtin", "available", "")
				if err != nil {
					t.Fatal(err)
				}
				if err := f.st.GrantAgentTool(ctx, agent.ID, id, "explicit"); err != nil {
					t.Fatal(err)
				}
			}
			f.svc.tools = recoverySurface{NewToolService(client, manager, f.st)}
			events := f.run(t, "cache-recovery-response")
			if callbackErr != nil {
				t.Fatal(callbackErr)
			}
			for _, event := range events {
				if event.IsError {
					t.Fatalf("tool failed: %+v", event)
				}
			}
			requests := f.provider.requestsSnapshot()
			seen := map[string]string{}
			for _, request := range requests {
				for _, name := range []string{"fetch_tool_result", "search_tool_result"} {
					if !containsToolNamed(request.Tools, name) {
						t.Fatalf("provider request lost %s", name)
					}
				}
				for _, message := range request.Messages {
					for _, block := range message.ContentBlocks {
						if block.Type == "tool_result" {
							seen[block.ToolUseID] = block.Content
						}
					}
				}
			}
			if !strings.Contains(seen["describe"], "json_pointer") {
				t.Fatal("discovery did not return the cache helper schema")
			}
			if strings.Contains(seen["source"], target) || !strings.Contains(seen["source"], "tool_result://") {
				t.Fatal("source did not exercise hidden-content recovery")
			}
			if !strings.Contains(seen["fetch"], target) {
				t.Fatalf("hidden evidence never reached the provider: %q", seen["fetch"])
			}
			if _, err := f.svc.resultCache.ReadPage("different-session", cacheID, pointer, 0, 0, 4000); err == nil {
				t.Fatal("session isolation failed")
			}
			snap := f.svc.inspector.Snapshot(f.session, "1")
			if snap == nil {
				t.Fatal("missing Inspector snapshot")
			}
			found := false
			for _, call := range snap.ToolCalls {
				if call.ToolID == "source" {
					found = true
					if call.Result != string(body) || call.VisibleResult == nil || *call.VisibleResult != seen["source"] || call.CacheID != cacheID {
						t.Fatal("Inspector did not retain the original and exact model-visible result")
					}
				}
			}
			if !found {
				t.Fatal("Inspector did not record the source call")
			}
		})
	}
}
