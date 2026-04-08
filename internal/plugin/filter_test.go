package plugin

import (
	"fmt"
	"strings"
	"testing"
)

func TestFilterRegistry_EmptyChainPassthrough(t *testing.T) {
	r := NewFilterRegistry()
	ctx := FilterContext{SessionID: "s1"}

	out, err := r.Apply("system_prompt", "hello", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello" {
		t.Errorf("expected passthrough, got %v", out)
	}
}

func TestFilterRegistry_SingleFilter(t *testing.T) {
	r := NewFilterRegistry()

	err := r.Register("system_prompt", "plugin-a", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		return data.(string) + " [filtered]", nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	out, err := r.Apply("system_prompt", "hello", FilterContext{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out != "hello [filtered]" {
		t.Errorf("expected 'hello [filtered]', got %v", out)
	}
}

func TestFilterRegistry_ChainOrdering(t *testing.T) {
	r := NewFilterRegistry()
	var order []string

	// Register in reverse priority order to verify sort.
	r.Register("test", "c", 30, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, "C")
		return data.(string) + "-C", nil
	})
	r.Register("test", "a", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, "A")
		return data.(string) + "-A", nil
	})
	r.Register("test", "b", 20, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, "B")
		return data.(string) + "-B", nil
	})

	out, err := r.Apply("test", "start", FilterContext{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if out != "start-A-B-C" {
		t.Errorf("expected 'start-A-B-C', got %v", out)
	}
	if len(order) != 3 || order[0] != "A" || order[1] != "B" || order[2] != "C" {
		t.Errorf("expected execution order [A B C], got %v", order)
	}
}

func TestFilterRegistry_ErrorAbortsChain(t *testing.T) {
	r := NewFilterRegistry()
	var ran []string

	r.Register("test", "a", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		ran = append(ran, "A")
		return data, nil
	})
	r.Register("test", "b", 20, func(data interface{}, ctx FilterContext) (interface{}, error) {
		ran = append(ran, "B")
		return nil, fmt.Errorf("b broke")
	})
	r.Register("test", "c", 30, func(data interface{}, ctx FilterContext) (interface{}, error) {
		ran = append(ran, "C")
		return data, nil
	})

	out, err := r.Apply("test", "start", FilterContext{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if out != nil {
		t.Errorf("expected nil data on error, got %v", out)
	}
	if !strings.Contains(err.Error(), "b broke") {
		t.Errorf("expected error to contain 'b broke', got %v", err)
	}
	// C should not have run.
	if len(ran) != 2 || ran[0] != "A" || ran[1] != "B" {
		t.Errorf("expected [A B] to run, got %v", ran)
	}
}

func TestFilterRegistry_SamePriorityKeepsInsertionOrder(t *testing.T) {
	r := NewFilterRegistry()
	var order []string

	r.Register("test", "first", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, "first")
		return data, nil
	})
	r.Register("test", "second", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		order = append(order, "second")
		return data, nil
	})

	_, err := r.Apply("test", "x", FilterContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("expected [first second], got %v", order)
	}
}

func TestFilterRegistry_ValidationErrors(t *testing.T) {
	r := NewFilterRegistry()

	if err := r.Register("", "p", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		return data, nil
	}); err == nil {
		t.Error("expected error for empty filter name")
	}

	if err := r.Register("test", "p", 10, nil); err == nil {
		t.Error("expected error for nil filter function")
	}
}

func TestFilterRegistry_ContextPassedToHandlers(t *testing.T) {
	r := NewFilterRegistry()

	var gotCtx FilterContext
	r.Register("test", "p", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		gotCtx = ctx
		return data, nil
	})

	fctx := FilterContext{
		SessionID: "s1",
		AgentID:   "a1",
		Metadata:  map[string]interface{}{"key": "val"},
	}
	r.Apply("test", "x", fctx)

	if gotCtx.SessionID != "s1" {
		t.Errorf("expected session s1, got %s", gotCtx.SessionID)
	}
	if gotCtx.AgentID != "a1" {
		t.Errorf("expected agent a1, got %s", gotCtx.AgentID)
	}
	if gotCtx.Metadata["key"] != "val" {
		t.Errorf("expected metadata key=val, got %v", gotCtx.Metadata)
	}
}

func TestFilterRegistry_Len(t *testing.T) {
	r := NewFilterRegistry()

	if r.Len("test") != 0 {
		t.Errorf("expected 0, got %d", r.Len("test"))
	}

	r.Register("test", "p", 10, func(data interface{}, ctx FilterContext) (interface{}, error) {
		return data, nil
	})
	if r.Len("test") != 1 {
		t.Errorf("expected 1, got %d", r.Len("test"))
	}

	r.Register("test", "q", 20, func(data interface{}, ctx FilterContext) (interface{}, error) {
		return data, nil
	})
	if r.Len("test") != 2 {
		t.Errorf("expected 2, got %d", r.Len("test"))
	}

	// Different chain name should be independent.
	if r.Len("other") != 0 {
		t.Errorf("expected 0 for 'other', got %d", r.Len("other"))
	}
}

func TestFilterView_Full_PassesAllData(t *testing.T) {
	r := NewFilterRegistry()

	data := map[string]interface{}{
		"user_message":      "hello",
		"tool_name":         "bash",
		"tool_input":        map[string]interface{}{"cmd": "ls"},
		"assistant_content": "I will run ls for you",
		"thinking":          "let me reason about this",
		"reasoning":         "step 1...",
	}

	var received interface{}
	r.RegisterWithView("test", "p", 10, FilterViewFull, func(d interface{}, ctx FilterContext) (interface{}, error) {
		received = d
		return d, nil
	})

	_, err := r.Apply("test", data, FilterContext{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	m := received.(map[string]interface{})
	for _, key := range []string{"user_message", "tool_name", "tool_input", "assistant_content", "thinking", "reasoning"} {
		if _, ok := m[key]; !ok {
			t.Errorf("full view should include key %q", key)
		}
	}
}

func TestFilterView_ReasoningBlind_StripsAssistantContent(t *testing.T) {
	r := NewFilterRegistry()

	data := map[string]interface{}{
		"user_message":      "hello",
		"tool_name":         "bash",
		"tool_input":        map[string]interface{}{"cmd": "ls"},
		"tool_call":         "call-123",
		"assistant_content": "I will run ls for you",
		"thinking":          "let me reason about this",
		"reasoning":         "step 1...",
	}

	var received interface{}
	r.RegisterWithView("test", "safety", 10, FilterViewReasoningBlind, func(d interface{}, ctx FilterContext) (interface{}, error) {
		received = d
		return d, nil
	})

	_, err := r.Apply("test", data, FilterContext{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	m := received.(map[string]interface{})

	// Should keep user/tool keys.
	for _, key := range []string{"user_message", "tool_name", "tool_input", "tool_call"} {
		if _, ok := m[key]; !ok {
			t.Errorf("reasoning-blind view should keep key %q", key)
		}
	}

	// Should strip reasoning keys.
	for _, key := range []string{"assistant_content", "thinking", "reasoning"} {
		if _, ok := m[key]; ok {
			t.Errorf("reasoning-blind view should strip key %q", key)
		}
	}

	// Original data must be unchanged.
	orig := data
	for _, key := range []string{"assistant_content", "thinking", "reasoning"} {
		if _, ok := orig[key]; !ok {
			t.Errorf("original data should still contain key %q", key)
		}
	}
}

func TestRegisterFilterWithView_DefaultsFull(t *testing.T) {
	r := NewFilterRegistry()

	data := map[string]interface{}{
		"user_message":      "hello",
		"assistant_content": "reasoning here",
		"thinking":          "thinking here",
	}

	var received interface{}
	// Register via the plain Register (no view) — should default to full.
	r.Register("test", "p", 10, func(d interface{}, ctx FilterContext) (interface{}, error) {
		received = d
		return d, nil
	})

	_, err := r.Apply("test", data, FilterContext{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	m := received.(map[string]interface{})
	// Full view: all keys should be present.
	for _, key := range []string{"user_message", "assistant_content", "thinking"} {
		if _, ok := m[key]; !ok {
			t.Errorf("default (full) view should include key %q", key)
		}
	}
}

func TestFilterChain_MixedViews(t *testing.T) {
	r := NewFilterRegistry()

	data := map[string]interface{}{
		"user_message":      "hello",
		"tool_name":         "bash",
		"assistant_content": "I will help",
		"thinking":          "let me think",
		"reasoning":         "step 1",
	}

	var fullSaw, blindSaw map[string]interface{}

	// Full-view filter at priority 10.
	r.RegisterWithView("test", "full-plugin", 10, FilterViewFull, func(d interface{}, ctx FilterContext) (interface{}, error) {
		m := d.(map[string]interface{})
		fullSaw = make(map[string]interface{}, len(m))
		for k, v := range m {
			fullSaw[k] = v
		}
		return d, nil
	})

	// Reasoning-blind filter at priority 20.
	r.RegisterWithView("test", "safety-plugin", 20, FilterViewReasoningBlind, func(d interface{}, ctx FilterContext) (interface{}, error) {
		m := d.(map[string]interface{})
		blindSaw = make(map[string]interface{}, len(m))
		for k, v := range m {
			blindSaw[k] = v
		}
		return d, nil
	})

	_, err := r.Apply("test", data, FilterContext{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Full-view filter should see everything.
	for _, key := range []string{"user_message", "tool_name", "assistant_content", "thinking", "reasoning"} {
		if _, ok := fullSaw[key]; !ok {
			t.Errorf("full-view filter should see key %q", key)
		}
	}

	// Reasoning-blind filter should see user/tool but NOT reasoning.
	for _, key := range []string{"user_message", "tool_name"} {
		if _, ok := blindSaw[key]; !ok {
			t.Errorf("reasoning-blind filter should see key %q", key)
		}
	}
	for _, key := range []string{"assistant_content", "thinking", "reasoning"} {
		if _, ok := blindSaw[key]; ok {
			t.Errorf("reasoning-blind filter should NOT see key %q", key)
		}
	}
}

func TestStripForView_NonMapData(t *testing.T) {
	// Non-map data should pass through unchanged for any view.
	s := "just a string"
	out := stripForView(s, FilterViewReasoningBlind)
	if out != s {
		t.Errorf("expected string passthrough, got %v", out)
	}
}

func TestFilterConstants_NonEmpty(t *testing.T) {
	constants := []string{
		FilterSystemPrompt,
		FilterUserMessage,
		FilterToolResult,
		FilterAssistantResponse,
		FilterContextWindow,
		FilterEnvelopeData,
	}
	for _, c := range constants {
		if c == "" {
			t.Error("filter constant is empty")
		}
	}
}
