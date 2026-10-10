package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/tool"
	toolresult "github.com/hollis-labs/substrate/agent/toolresult"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestCacheProjection_ResumeAndClear(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "resume.db")
	st, err := storetest.New(t, ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	session := &store.Session{}
	if err = st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	agent := &store.AgentProfile{Name: "Reader", Slug: "reader", SystemPrompt: "SYSTEM SURVIVES"}
	if err = storetest.PriorAuthorizedActor(t.Context(), st, agent); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSessionContextPrompt(ctx, session.ID, "PIN SURVIVES"); err != nil {
		t.Fatal(err)
	}
	cache := tool.NewResultCache(st.DB, tool.ResultCacheConfig{})
	view, err := cache.Results.Present(ctx, session.ID, toolresult.Meta{CallID: "read", Tool: "read_source"}, strings.Repeat("retained preview\n", 1000), 500)
	if err != nil {
		t.Fatal(err)
	}
	msg := &store.Message{SessionID: session.ID, Role: "assistant", Content: view.Content}
	if err = st.CreateMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	initial, err := NewContextClient(st).AssembleSlotSources(ctx, session, agent)
	if err != nil || initial.Messages[0].Content != view.Content {
		t.Fatalf("live assembly: %v", err)
	}
	if _, err = st.DB.Exec(`UPDATE tool_result_cache SET expires_at = '2020-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = store.New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(ctx)
	for _, phase := range []string{"expired after reopen", "purged after reopen"} {
		if phase == "purged after reopen" {
			if _, err = tool.PurgeExpired(st.DB); err != nil {
				t.Fatal(err)
			}
		}
		sources, assembleErr := NewContextClient(st).AssembleSlotSources(ctx, session, agent)
		if assembleErr != nil {
			t.Fatal(assembleErr)
		}
		got := sources.Messages[0].Content
		if strings.Contains(got, "tool_result://") || !strings.Contains(got, "CACHED RESULT UNAVAILABLE") || !strings.Contains(got, "retained preview") {
			t.Fatalf("%s: %s", phase, got)
		}
		if !strings.Contains(sources.Agent, "SYSTEM SURVIVES") || !strings.Contains(sources.UserContext, "PIN SURVIVES") {
			t.Fatal("static context changed")
		}
		raw, readErr := st.ListMessages(ctx, session.ID, 200)
		if readErr != nil || raw[0].Content != view.Content {
			t.Fatal("transcript changed")
		}
	}
	if _, err = st.ClearConversation(ctx, session.ID, false); err != nil {
		t.Fatal(err)
	}
	sources, err := NewContextClient(st).AssembleSlotSources(ctx, session, agent)
	if err != nil || len(sources.Messages) != 0 {
		t.Fatalf("clear revived cached history: %v", err)
	}
}

func TestCacheProjection_CopyAndPrune(t *testing.T) {
	original := strings.Repeat("old source details ", 30) + "\n\n[TRUNCATED — full result cached as tool_result://gone; use fetch_tool_result]"
	messages := []llmtypes.ChatMessage{{Role: "assistant", Content: original, ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "old", Content: original}}}}
	projected := ReconcileCachedResults(context.Background(), nil, "owner", messages)
	if projected[0].Content == original || projected[0].ContentBlocks[0].Content == original {
		t.Fatal("plain/block projection not reconciled")
	}
	if messages[0].Content != original || messages[0].ContentBlocks[0].Content != original {
		t.Fatal("original request snapshot mutated")
	}
	projected = append(projected, llmtypes.ChatMessage{ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", Content: "recent"}}}, llmtypes.ChatMessage{ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", Content: "latest"}}})
	pruned := pruneToolResultsInMemory(projected)
	if !strings.Contains(pruned[0].ContentBlocks[0].Content, "CACHED RESULT UNAVAILABLE") || !strings.HasPrefix(pruned[0].ContentBlocks[0].Content, "[pruned:") {
		t.Fatal("pruning hid degradation")
	}
}
