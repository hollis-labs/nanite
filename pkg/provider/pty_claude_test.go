package provider

import (
	"testing"
)

func TestParseClaudeStreamLine_Empty(t *testing.T) {
	events, err := parseClaudeStreamLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_SystemEvent(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","cwd":"/tmp","session_id":"abc","tools":["Read","Write"],"model":"claude-opus-4-6"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for system init, got %d", len(events))
	}
	if events[0].Type != "session_id" {
		t.Errorf("expected type=session_id, got %s", events[0].Type)
	}
	if events[0].SessionID != "abc" {
		t.Errorf("expected SessionID=abc, got %s", events[0].SessionID)
	}
}

func TestParseClaudeStreamLine_SystemEventNonInit(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"api_retry","session_id":"abc"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for non-init system event, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_SystemEventNoSessionID(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","cwd":"/tmp"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for init without session_id, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_AssistantText(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello there!"}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "Hello there!" {
		t.Errorf("expected content='Hello there!', got %q", events[0].Content)
	}
}

func TestParseClaudeStreamLine_AssistantToolUse(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_123","name":"Read","input":{"file_path":"/tmp/test.txt"}}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "tool_use" {
		t.Errorf("expected type=tool_use, got %s", events[0].Type)
	}
	if events[0].ToolUse == nil {
		t.Fatal("expected non-nil ToolUse")
	}
	if events[0].ToolUse.Name != "Read" {
		t.Errorf("expected tool name=Read, got %s", events[0].ToolUse.Name)
	}
	if events[0].ToolUse.ID != "tu_123" {
		t.Errorf("expected tool id=tu_123, got %s", events[0].ToolUse.ID)
	}
	fp, ok := events[0].ToolUse.Input["file_path"]
	if !ok || fp != "/tmp/test.txt" {
		t.Errorf("expected input file_path=/tmp/test.txt, got %v", fp)
	}
}

func TestParseClaudeStreamLine_AssistantMixed(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me read that file."},{"type":"tool_use","id":"tu_456","name":"Bash","input":{"command":"ls"}}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("events[0]: expected type=delta, got %s", events[0].Type)
	}
	if events[1].Type != "tool_use" {
		t.Errorf("events[1]: expected type=tool_use, got %s", events[1].Type)
	}
}

func TestParseClaudeStreamLine_ResultSuccess(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Done.","stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":500,"cache_read_input_tokens":0}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (usage + done), got %d", len(events))
	}
	if events[0].Type != "usage" {
		t.Errorf("events[0]: expected type=usage, got %s", events[0].Type)
	}
	if events[0].Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	if events[0].Usage.InputTokens != 100 {
		t.Errorf("expected input_tokens=100, got %d", events[0].Usage.InputTokens)
	}
	if events[0].Usage.OutputTokens != 20 {
		t.Errorf("expected output_tokens=20, got %d", events[0].Usage.OutputTokens)
	}
	if events[0].Usage.CacheCreationTokens != 500 {
		t.Errorf("expected cache_creation_tokens=500, got %d", events[0].Usage.CacheCreationTokens)
	}
	if events[0].Usage.StopReason != "end_turn" {
		t.Errorf("expected stop_reason=end_turn, got %s", events[0].Usage.StopReason)
	}
	if events[1].Type != "done" {
		t.Errorf("events[1]: expected type=done, got %s", events[1].Type)
	}
}

func TestParseClaudeStreamLine_ResultError(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"error","is_error":true,"result":"Something went wrong"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
	if events[0].Error != "Something went wrong" {
		t.Errorf("expected error message, got %q", events[0].Error)
	}
}

func TestParseClaudeStreamLine_TopLevelError(t *testing.T) {
	line := []byte(`{"type":"error","error":{"message":"API key invalid"}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
	if events[0].Error != "API key invalid" {
		t.Errorf("expected error='API key invalid', got %q", events[0].Error)
	}
}

func TestParseClaudeStreamLine_RateLimitEvent(t *testing.T) {
	line := []byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for rate_limit_event, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_UnknownType(t *testing.T) {
	line := []byte(`{"type":"future_event","data":"something"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown type, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_InvalidJSON(t *testing.T) {
	line := []byte(`not valid json`)
	_, err := parseClaudeStreamLine(line)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestParseClaudeAssistant_ToolOnlyProducesNoDeltas verifies that a Claude
// assistant message containing only tool_use blocks emits tool_use events but
// zero delta events. This documents the parser behaviour that the PTY/subprocess
// bridge relies on when applying the tool-only silence detection: if seenDelta
// remains 0 after the stream closes, the bridge emits a visible error instead
// of leaving the user with an empty assistant row.
func TestParseClaudeAssistant_ToolOnlyProducesNoDeltas(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_abc","name":"Bash","input":{"command":"ls /tmp"}}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event (tool_use), got %d", len(events))
	}
	if events[0].Type != "tool_use" {
		t.Errorf("expected type=tool_use, got %s", events[0].Type)
	}
	// No delta events — this is what triggers the bridge's error injection.
	for _, ev := range events {
		if ev.Type == "delta" {
			t.Errorf("unexpected delta event in tool-only message")
		}
	}
}

// TestParseClaudeAssistant_MixedBlocksProducesDelta verifies that a Claude
// assistant message with both text and tool_use blocks emits at least one
// delta event — the bridge will NOT emit a tool-proxy error in this case.
func TestParseClaudeAssistant_MixedBlocksProducesDelta(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me check that."},{"type":"tool_use","id":"tu_xyz","name":"Read","input":{"file_path":"/tmp/x"}}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasDelta := false
	hasToolUse := false
	for _, ev := range events {
		switch ev.Type {
		case "delta":
			hasDelta = true
		case "tool_use":
			hasToolUse = true
		}
	}
	if !hasDelta {
		t.Error("expected at least one delta event for mixed-block message")
	}
	if !hasToolUse {
		t.Error("expected at least one tool_use event for mixed-block message")
	}
}
