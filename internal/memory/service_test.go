package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// mockMCPCaller records calls and returns configured responses.
type mockMCPCaller struct {
	calls    []mcpCall
	response string
	err      error
}

type mcpCall struct {
	Name  string
	Input map[string]any
}

func (m *mockMCPCaller) ExecuteTool(_ context.Context, name string, input map[string]any) (string, error) {
	m.calls = append(m.calls, mcpCall{Name: name, Input: input})
	return m.response, m.err
}

func TestMemoryStore(t *testing.T) {
	mock := &mockMCPCaller{response: `{"ok": true}`}
	svc := NewService(mock)

	err := svc.Store(context.Background(), Memory{
		Namespace:  "app/nanite/session/test-123",
		MemoryKey:  "prefers_terse_output",
		Summary:    "User prefers terse output",
		Body:       "When asked, user said they prefer concise responses.",
		Origin:     "user",
		Trigger:    "per_turn",
		Confidence: 0.9,
		Tags:       []string{"preferences", "output-style"},
		SessionID:  "test-123",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 MCP call, got %d", len(mock.calls))
	}

	call := mock.calls[0]
	if call.Name != "mcp__conduit__memory_write" {
		t.Errorf("expected tool name mcp__conduit__memory_write, got %s", call.Name)
	}
	if call.Input["namespace"] != "app/nanite/session/test-123" {
		t.Errorf("expected namespace app/nanite/session/test-123, got %v", call.Input["namespace"])
	}
	if call.Input["memory_key"] != "prefers_terse_output" {
		t.Errorf("expected memory_key prefers_terse_output, got %v", call.Input["memory_key"])
	}
	if call.Input["origin"] != "user" {
		t.Errorf("expected origin user, got %v", call.Input["origin"])
	}
	if call.Input["trigger"] != "per_turn" {
		t.Errorf("expected trigger per_turn, got %v", call.Input["trigger"])
	}
	if call.Input["confidence"] != 0.9 {
		t.Errorf("expected confidence 0.9, got %v", call.Input["confidence"])
	}
}

func TestMemoryStore_NilMCP(t *testing.T) {
	svc := NewService(nil)
	err := svc.Store(context.Background(), Memory{Summary: "test"})
	if err == nil {
		t.Error("expected error for nil MCP caller")
	}
}

func TestMemoryStore_MCPError(t *testing.T) {
	mock := &mockMCPCaller{err: fmt.Errorf("connection refused")}
	svc := NewService(mock)

	err := svc.Store(context.Background(), Memory{
		Namespace: "app/nanite/test",
		MemoryKey: "test",
		Summary:   "test",
	})

	if err == nil {
		t.Error("expected error when MCP call fails")
	}
}

func TestMemoryRecall(t *testing.T) {
	memories := []Memory{
		{
			Namespace:  "app/nanite/user/chrispian",
			MemoryKey:  "prefers_terse_output",
			Summary:    "User prefers terse output",
			Origin:     "user",
			Confidence: 0.9,
		},
		{
			Namespace:  "app/nanite/project/nanite",
			MemoryKey:  "uses_sqlite",
			Summary:    "Project uses SQLite for persistence",
			Origin:     "project",
			Confidence: 0.95,
		},
	}

	responseJSON, _ := json.Marshal(memories)
	mock := &mockMCPCaller{response: string(responseJSON)}
	svc := NewService(mock)

	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces:    []string{"app/nanite/user/chrispian", "app/nanite/project/nanite"},
		Ranking:       "activation",
		Limit:         10,
		MinConfidence: 0.5,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(results))
	}

	if results[0].Summary != "User prefers terse output" {
		t.Errorf("unexpected first memory summary: %s", results[0].Summary)
	}

	// Verify MCP call parameters.
	call := mock.calls[0]
	if call.Name != "mcp__conduit__memory_recall" {
		t.Errorf("expected tool name mcp__conduit__memory_recall, got %s", call.Name)
	}
	if call.Input["ranking"] != "activation" {
		t.Errorf("expected ranking activation, got %v", call.Input["ranking"])
	}
	if call.Input["limit"] != 10 {
		t.Errorf("expected limit 10, got %v", call.Input["limit"])
	}
}

func TestMemoryRecall_WrappedFormat(t *testing.T) {
	response := `{"memories": [{"namespace": "app/nanite/user/test", "memory_key": "test_key", "summary": "A test memory"}]}`
	mock := &mockMCPCaller{response: response}
	svc := NewService(mock)

	results, err := svc.Recall(context.Background(), RecallOpts{
		Namespaces: []string{"app/nanite/user/test"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(results))
	}
	if results[0].MemoryKey != "test_key" {
		t.Errorf("expected memory_key test_key, got %s", results[0].MemoryKey)
	}
}

func TestMemoryRecall_DefaultValues(t *testing.T) {
	mock := &mockMCPCaller{response: "[]"}
	svc := NewService(mock)

	_, err := svc.Recall(context.Background(), RecallOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := mock.calls[0]
	// Default ranking should be "activation".
	if call.Input["ranking"] != "activation" {
		t.Errorf("expected default ranking activation, got %v", call.Input["ranking"])
	}
	// Default limit should be 20.
	if call.Input["limit"] != 20 {
		t.Errorf("expected default limit 20, got %v", call.Input["limit"])
	}
}

func TestPerTurnExtraction_WithSignal(t *testing.T) {
	if !HasMemorySignal("Please remember this: I always prefer tabs over spaces") {
		t.Error("expected HasMemorySignal to detect 'remember' and 'always'")
	}
	if !HasMemorySignal("From now on, use Go 1.22 features") {
		t.Error("expected HasMemorySignal to detect 'from now on'")
	}
	if !HasMemorySignal("I prefer using SQLite for small projects") {
		t.Error("expected HasMemorySignal to detect 'I prefer'")
	}
	if !HasMemorySignal("No, don't do that. Use the other approach.") {
		t.Error("expected HasMemorySignal to detect correction pattern")
	}
	if !HasMemorySignal("Never use global variables in this project") {
		t.Error("expected HasMemorySignal to detect 'never'")
	}
}

func TestPerTurnExtraction_NoSignal(t *testing.T) {
	if HasMemorySignal("Can you help me write a function to parse JSON?") {
		t.Error("ordinary request should not trigger memory signal")
	}
	if HasMemorySignal("What is the capital of France?") {
		t.Error("simple question should not trigger memory signal")
	}
	if HasMemorySignal("Please review this code for bugs") {
		t.Error("code review request should not trigger memory signal")
	}
}

func TestPostCompactionExtraction(t *testing.T) {
	// Verify the extractor creates a valid hook without error.
	storeMock := &mockMCPCaller{response: `{"ok": true}`}
	svc := NewService(storeMock)

	called := false
	utilityCall := func(_ context.Context, prompt string) (string, error) {
		called = true
		return `[{"memory_key": "test", "summary": "A test memory", "origin": "project", "confidence": 0.9, "tags": ["test"]}]`, nil
	}

	extractor := NewExtractor(svc, utilityCall)
	// Directly test the extractPostCompact method.
	extractor.extractPostCompact("session-123", 5000)

	if !called {
		t.Error("expected utility call to be made during post-compact extraction")
	}

	if len(storeMock.calls) != 1 {
		t.Fatalf("expected 1 store call, got %d", len(storeMock.calls))
	}

	call := storeMock.calls[0]
	if call.Input["namespace"] != "app/nanite/session/session-123" {
		t.Errorf("expected session namespace, got %v", call.Input["namespace"])
	}
	if call.Input["trigger"] != "post_compact" {
		t.Errorf("expected trigger post_compact, got %v", call.Input["trigger"])
	}
}

func TestPerTurnExtraction_Full(t *testing.T) {
	storeMock := &mockMCPCaller{response: `{"ok": true}`}
	svc := NewService(storeMock)

	utilityCall := func(_ context.Context, prompt string) (string, error) {
		return `{"memory_key": "prefers_terse", "summary": "User prefers terse output", "origin": "user", "confidence": 0.85, "tags": ["preferences"]}`, nil
	}

	extractor := NewExtractor(svc, utilityCall)
	extractor.extractPerTurn("session-456", "I always prefer terse, concise responses.")

	if len(storeMock.calls) != 1 {
		t.Fatalf("expected 1 store call, got %d", len(storeMock.calls))
	}

	call := storeMock.calls[0]
	if call.Input["memory_key"] != "prefers_terse" {
		t.Errorf("expected memory_key prefers_terse, got %v", call.Input["memory_key"])
	}
	if call.Input["origin"] != "user" {
		t.Errorf("expected origin user, got %v", call.Input["origin"])
	}
}

func TestPerTurnExtraction_LowConfidence(t *testing.T) {
	storeMock := &mockMCPCaller{response: `{"ok": true}`}
	svc := NewService(storeMock)

	utilityCall := func(_ context.Context, prompt string) (string, error) {
		return `{"memory_key": "maybe", "summary": "Maybe important", "origin": "user", "confidence": 0.3, "tags": []}`, nil
	}

	extractor := NewExtractor(svc, utilityCall)
	extractor.extractPerTurn("session-789", "Remember this might be useful")

	if len(storeMock.calls) != 0 {
		t.Error("expected no store calls for low-confidence extraction")
	}
}

func TestNamespaceHelpers(t *testing.T) {
	if ns := SessionNamespace("abc-123"); ns != "app/nanite/session/abc-123" {
		t.Errorf("unexpected session namespace: %s", ns)
	}
	if ns := ProjectNamespace("nanite"); ns != "app/nanite/project/nanite" {
		t.Errorf("unexpected project namespace: %s", ns)
	}
	if ns := UserNamespace("chrispian"); ns != "app/nanite/user/chrispian" {
		t.Errorf("unexpected user namespace: %s", ns)
	}
}

func TestCleanJSONResponse(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"key": "value"}`, `{"key": "value"}`},
		{"```json\n{\"key\": \"value\"}\n```", `{"key": "value"}`},
		{"  \n```\n{\"key\": \"value\"}\n```\n  ", `{"key": "value"}`},
		{`  {"key": "value"}  `, `{"key": "value"}`},
	}

	for _, tt := range tests {
		got := cleanJSONResponse(tt.input)
		if got != tt.want {
			t.Errorf("cleanJSONResponse(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseRecallResult(t *testing.T) {
	// Array format.
	t.Run("array", func(t *testing.T) {
		input := `[{"namespace": "ns", "memory_key": "k", "summary": "s"}]`
		memories, err := parseRecallResult(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(memories) != 1 {
			t.Fatalf("expected 1 memory, got %d", len(memories))
		}
	})

	// Wrapped format with "memories" key.
	t.Run("wrapped_memories", func(t *testing.T) {
		input := `{"memories": [{"namespace": "ns", "memory_key": "k", "summary": "s"}]}`
		memories, err := parseRecallResult(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(memories) != 1 {
			t.Fatalf("expected 1 memory, got %d", len(memories))
		}
	})

	// Wrapped format with "results" key.
	t.Run("wrapped_results", func(t *testing.T) {
		input := `{"results": [{"namespace": "ns", "memory_key": "k", "summary": "s"}]}`
		memories, err := parseRecallResult(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(memories) != 1 {
			t.Fatalf("expected 1 memory, got %d", len(memories))
		}
	})

	// Empty.
	t.Run("empty", func(t *testing.T) {
		memories, err := parseRecallResult("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(memories) != 0 {
			t.Errorf("expected 0 memories, got %d", len(memories))
		}
	})
}
