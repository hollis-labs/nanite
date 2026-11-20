package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/toolchoice"
)

func TestClosingRequestKeepsToolsAndDisablesCalls(t *testing.T) {
	req := llmtypes.ChatRequest{Model: "test", MaxTokens: 20, Tools: []llmtypes.ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}, Messages: []llmtypes.ChatMessage{{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: "call", Name: "lookup"}}}, {Role: "user", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "call", Content: "found"}}}}, CacheHints: []llmtypes.CacheHint{{Position: "tools"}}}
	c := New()
	params := c.buildRequestParams(toolchoice.WithNone(t.Context()), req, "test", false, llmcontracts.ReasoningConfig{})
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tools":[`, `"tool_choice":{"type":"none"}`, `"cache_control":{"type":"ephemeral"}`, `"tool_use"`, `"tool_result"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s: %s", want, raw)
		}
	}
	normal, err := json.Marshal(c.buildRequestParams(t.Context(), req, "test", false, llmcontracts.ReasoningConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(normal), `"tool_choice"`) {
		t.Fatalf("normal request changed: %s", normal)
	}
}
