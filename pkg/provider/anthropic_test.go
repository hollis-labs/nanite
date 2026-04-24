package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildSystemBlocks(t *testing.T) {
	t.Run("empty prompt returns nil", func(t *testing.T) {
		blocks := buildSystemBlocks("")
		if blocks != nil {
			t.Fatalf("expected nil, got %v", blocks)
		}
	})

	t.Run("non-empty prompt has cache_control", func(t *testing.T) {
		blocks := buildSystemBlocks("You are a helpful assistant.")
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		block := blocks[0]
		if block["type"] != "text" {
			t.Errorf("expected type=text, got %v", block["type"])
		}
		if block["text"] != "You are a helpful assistant." {
			t.Errorf("unexpected text: %v", block["text"])
		}
		cc, ok := block["cache_control"].(map[string]string)
		if !ok {
			t.Fatalf("cache_control missing or wrong type: %v", block["cache_control"])
		}
		if cc["type"] != "ephemeral" {
			t.Errorf("expected ephemeral, got %v", cc["type"])
		}
	})
}

func TestBuildToolsWithCacheControl(t *testing.T) {
	t.Run("empty tools returns nil", func(t *testing.T) {
		result := buildToolsWithCacheControl(nil)
		if result != nil {
			t.Fatalf("expected nil, got %v", result)
		}
	})

	t.Run("last tool gets cache_control", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "tool_a", Description: "First tool", InputSchema: map[string]any{"type": "object"}},
			{Name: "tool_b", Description: "Second tool", InputSchema: map[string]any{"type": "object"}},
		}
		result := buildToolsWithCacheControl(tools)
		if len(result) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(result))
		}

		// First tool should NOT have cache_control.
		first := result[0].(map[string]any)
		if _, ok := first["cache_control"]; ok {
			t.Error("first tool should not have cache_control")
		}

		// Last tool should have cache_control.
		last := result[1].(map[string]any)
		cc, ok := last["cache_control"].(map[string]string)
		if !ok {
			t.Fatalf("last tool missing cache_control: %v", last)
		}
		if cc["type"] != "ephemeral" {
			t.Errorf("expected ephemeral, got %v", cc["type"])
		}
	})

	t.Run("single tool gets cache_control", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "tool_a", Description: "Only tool", InputSchema: map[string]any{"type": "object"}},
		}
		result := buildToolsWithCacheControl(tools)
		only := result[0].(map[string]any)
		if _, ok := only["cache_control"]; !ok {
			t.Error("single tool should have cache_control")
		}
	})
}

func TestMarshalMessagesCacheControl(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "Great!"},
		{Role: "user", Content: "Tell me a joke"},
	}

	result := marshalMessages(messages)

	// Last 2 user messages are at indices 2 and 4.
	// Index 0 (first user) should be plain string.
	first := result[0].(map[string]any)
	if _, ok := first["content"].(string); !ok {
		t.Errorf("first user message should have plain string content, got %T", first["content"])
	}

	// Index 2 (second user) should have cache_control.
	second := result[2].(map[string]any)
	blocks, ok := second["content"].([]map[string]any)
	if !ok {
		t.Fatalf("expected content blocks at index 2, got %T", second["content"])
	}
	if _, ok := blocks[0]["cache_control"]; !ok {
		t.Error("second-to-last user message should have cache_control")
	}

	// Index 4 (last user) should have cache_control.
	last := result[4].(map[string]any)
	blocks, ok = last["content"].([]map[string]any)
	if !ok {
		t.Fatalf("expected content blocks at index 4, got %T", last["content"])
	}
	if _, ok := blocks[0]["cache_control"]; !ok {
		t.Error("last user message should have cache_control")
	}

	// Assistant messages should not have cache_control.
	assistant := result[1].(map[string]any)
	if _, ok := assistant["content"].(string); !ok {
		t.Errorf("assistant message should have plain string content, got %T", assistant["content"])
	}
}

// TestAnthropicTaskBudget verifies the task_budget constant is set to the
// expected default (64 K tokens) and that the combined beta header string
// includes both the prompt-caching and task-budgets beta identifiers.
func TestAnthropicTaskBudget(t *testing.T) {
	t.Run("task_budget_tokens constant is 64000", func(t *testing.T) {
		if anthropicTaskBudgetTokens != 64_000 {
			t.Errorf("expected anthropicTaskBudgetTokens=64000, got %d", anthropicTaskBudgetTokens)
		}
	})

	t.Run("task_budget param marshals correctly", func(t *testing.T) {
		budget := map[string]any{
			"type":  "tokens",
			"total": anthropicTaskBudgetTokens,
		}
		data, err := json.Marshal(budget)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if got["type"] != "tokens" {
			t.Errorf("expected type=tokens, got %v", got["type"])
		}
		// JSON numbers unmarshal as float64.
		total, ok := got["total"].(float64)
		if !ok {
			t.Fatalf("expected numeric total, got %T", got["total"])
		}
		if int(total) != 64_000 {
			t.Errorf("expected total=64000, got %v", total)
		}
	})

	t.Run("beta header string contains both required betas", func(t *testing.T) {
		// Assert against the production constant so a change to AnthropicBetaHeaders
		// in anthropic.go will break this test, not silently pass.
		if !strings.Contains(AnthropicBetaHeaders, "prompt-caching-2024-07-31") {
			t.Error("AnthropicBetaHeaders missing prompt-caching-2024-07-31")
		}
		if !strings.Contains(AnthropicBetaHeaders, "task-budgets-2026-03-13") {
			t.Error("AnthropicBetaHeaders missing task-budgets-2026-03-13")
		}
	})
}

func TestAnthropicRequestJSON(t *testing.T) {
	// Verify that the SDK-param system block marshals with cache_control.
	// (Previously this test constructed an anthropicRequest struct; the SDK
	// replaces that with anthropic.MessageNewParams. The semantic being
	// checked — system blocks carry cache_control ephemeral — is preserved.)
	a := NewAnthropic()
	a.SetCacheHints(DefaultCacheStrategy())
	system := a.buildSDKSystem("test system")
	if len(system) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(system))
	}
	data, err := json.Marshal(system[0])
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var block map[string]any
	if err := json.Unmarshal(data, &block); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if block["type"] != "text" {
		t.Errorf("expected type=text, got %v", block["type"])
	}
	if block["text"] != "test system" {
		t.Errorf("expected text=test system, got %v", block["text"])
	}
	cc, ok := block["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("cache_control missing: %v", block["cache_control"])
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("expected ephemeral cache_control, got %v", cc["type"])
	}
}
