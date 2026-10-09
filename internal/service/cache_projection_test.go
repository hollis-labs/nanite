package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tool"
	toolresult "github.com/hollis-labs/substrate/agent/toolresult"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestCacheProjection_RechecksEveryProviderIteration(t *testing.T) {
	for _, state := range []string{"expired", "purged"} {
		t.Run(state, func(t *testing.T) {
			var f *characterizationFixture
			var callbackErr error
			steps := []characterizationProviderStep{
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "echo", Name: "echo", Input: map[string]any{}}), beforeReturn: func() {
					sql := `UPDATE tool_result_cache SET expires_at = '2020-01-01T00:00:00Z'`
					if state == "purged" {
						sql = `DELETE FROM tool_result_cache`
					}
					_, callbackErr = f.st.DB.Exec(sql)
				}},
				{events: doneEvents("The previous preview is incomplete evidence.")},
			}
			f = newCharacterizationFixture(t, steps, "echo")
			f.svc.resultCache = tool.NewResultCache(f.st.DB, tool.ResultCacheConfig{})
			view, err := f.svc.resultCache.Results.Present(context.Background(), f.session, toolresult.Meta{CallID: "prior-source", Tool: "read_source"}, strings.Repeat("partial source ", 1000), 500)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.st.CreateMessage(context.Background(), &store.Message{SessionID: f.session, Role: "assistant", Content: view.Content}); err != nil {
				t.Fatal(err)
			}
			events := f.run(t, "recheck-response")
			if callbackErr != nil {
				t.Fatal(callbackErr)
			}
			requests := f.provider.requestsSnapshot()
			if len(requests) != 2 {
				t.Fatalf("requests=%d events=%v", len(requests), eventTypes(events))
			}
			first, second := "", ""
			for _, m := range requests[0].Messages {
				first += m.Content
			}
			for _, m := range requests[1].Messages {
				second += m.Content
			}
			if !strings.Contains(first, "tool_result://"+view.CacheID) {
				t.Fatal("live pointer was not provided")
			}
			if strings.Contains(second, "tool_result://"+view.CacheID) || !strings.Contains(second, "CACHED RESULT UNAVAILABLE") {
				t.Fatalf("next request did not reconcile %s: %s", state, second)
			}
			// Prior request and durable transcript stay exact; source outcome is not
			// retroactively changed into a failed execution when retention ends.
			raw, err := f.st.ListMessages(context.Background(), f.session, 200)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, msg := range raw {
				if msg.Content == view.Content {
					found = true
				}
			}
			if !found {
				t.Fatal("prior source transcript rewritten")
			}
			if calls := f.tools.calls(); len(calls) != 1 || calls[0] != "echo" {
				t.Fatal("source was silently re-executed")
			}
			for _, event := range events {
				if event.IsError {
					t.Fatalf("retention changed turn/tool outcome: %+v", event)
				}
			}
		})
	}
}

func TestCacheProjection_CLIRecoveryAndRetainedHistory(t *testing.T) {
	st := newConfigTestStore(t)
	ctx := context.Background()
	session := &store.Session{Title: "Cache resume", Provider: "claude", Model: "claude-sonnet"}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	svc := &chatServiceImpl{store: st, resultCache: tool.NewResultCache(st.DB, tool.ResultCacheConfig{})}
	view, err := svc.resultCache.Results.Present(ctx, session.ID, toolresult.Meta{CallID: "source", Tool: "read_source"}, strings.Repeat("long retained preview ", 500), 3000)
	if err != nil {
		t.Fatal(err)
	}
	msg := &store.Message{SessionID: session.ID, Role: "assistant", Content: view.Content}
	if err = st.CreateMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`DELETE FROM tool_result_cache`); err != nil {
		t.Fatal(err)
	}
	bootDir := t.TempDir()
	for _, cold := range []bool{true, false} {
		payload := svc.composeBootPayload(session.ID, session, nil, bootDir, nil, "continue", cold)
		if strings.Contains(payload, "tool_result://"+view.CacheID) || !strings.Contains(payload, "CACHED RESULT UNAVAILABLE: "+view.CacheID) {
			t.Fatalf("cold=%v stale history advertised: %s", cold, payload)
		}
		if !strings.Contains(payload, "Only a successful current-session fetch/search") || !strings.HasSuffix(payload, "continue") {
			t.Fatal("opaque provider-history correction/new user message missing")
		}
	}
	root, err := os.OpenRoot(bootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := root.ReadFile(recoveryPackFileName)
	if err != nil || strings.Contains(string(file), "tool_result://"+view.CacheID) || !strings.Contains(string(file), "CACHED RESULT UNAVAILABLE") {
		t.Fatalf("long recovery file lost degradation notice: %v", err)
	}
	raw, err := st.ListMessages(ctx, session.ID, 200)
	if err != nil || raw[0].Content != view.Content {
		t.Fatal("full transcript changed")
	}
	if _, err = st.ClearConversation(ctx, session.ID, false); err != nil {
		t.Fatal(err)
	}
	payload := svc.composeBootPayload(session.ID, session, nil, t.TempDir(), nil, "new conversation", true)
	if strings.Contains(payload, view.CacheID) || strings.Contains(payload, "<recovered-session-context>") {
		t.Fatal("clear revived prior cache references")
	}
}
