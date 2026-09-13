package chat

import (
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

func TestEnforceTokenBudget_KeepsCacheNavigation(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "ordinary", Description: strings.Repeat("large schema ", 1000)},
		{Name: "fetch_tool_result", Description: "Read cached output"},
		{Name: "search_tool_result", Description: "Search cached output"},
	}
	_, kept, _, err := EnforceTokenBudget("system", []llmtypes.ChatMessage{{Role: "user", Content: "recover source"}}, tools, 500)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, def := range kept {
		seen[def.Name] = true
	}
	if !seen["fetch_tool_result"] || !seen["search_tool_result"] || seen["ordinary"] {
		t.Fatalf("budget reduction lost cache navigation: %v", seen)
	}
}

func TestPruneToolResults_KeepsRecoveryPointer(t *testing.T) {
	footer := "\n\n[TRUNCATED — full result cached as tool_result://example; use fetch_tool_result or search_tool_result]"
	original := strings.Repeat("source details ", 500) + footer
	msgs := []llmtypes.ChatMessage{}
	for _, id := range []string{"old", "recent", "latest"} {
		msgs = append(msgs,
			llmtypes.ChatMessage{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: id, Name: "source"}}},
			llmtypes.ChatMessage{Role: "user", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: id, Content: original}}},
		)
	}
	pruned := pruneToolResultsInMemory(msgs)
	content := pruned[1].ContentBlocks[0].Content
	if !strings.HasPrefix(content, "[pruned:") || !strings.HasSuffix(content, footer) || len(content) >= len(original) {
		t.Fatalf("old source is no longer recoverable: %q", content)
	}
	if msgs[1].ContentBlocks[0].Content != original {
		t.Fatal("pruning mutated the original messages")
	}
	if again := pruneToolResultsInMemory(pruned); again[1].ContentBlocks[0].Content != content {
		t.Fatal("repeated pruning discarded or rewrote the recovery pointer")
	}
}
