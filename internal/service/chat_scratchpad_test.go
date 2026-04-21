package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
)

func newTestLoopState() *loopState {
	return newLoopState(chat.AgentConstraints{}, nil, false)
}

func TestCallScratchpadWrite_Success(t *testing.T) {
	ls := newTestLoopState()
	result, isErr := callScratchpadWrite(map[string]any{"key": "k", "value": "hello"}, ls)
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if out["stored"] != true {
		t.Errorf("expected stored=true, got %v", out["stored"])
	}
}

func TestCallScratchpadWrite_MissingKey(t *testing.T) {
	ls := newTestLoopState()
	_, isErr := callScratchpadWrite(map[string]any{"value": "x"}, ls)
	if !isErr {
		t.Fatal("expected error for missing key")
	}
}

func TestCallScratchpadWrite_MissingValue(t *testing.T) {
	ls := newTestLoopState()
	_, isErr := callScratchpadWrite(map[string]any{"key": "k"}, ls)
	if !isErr {
		t.Fatal("expected error for missing value")
	}
}

func TestCallScratchpadWrite_ValueTooLarge(t *testing.T) {
	ls := newTestLoopState()
	big := make([]byte, scratchpadMaxValueBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	_, isErr := callScratchpadWrite(map[string]any{"key": "k", "value": string(big)}, ls)
	if !isErr {
		t.Fatal("expected error for oversized value")
	}
}

func TestCallScratchpadRead_SpecificKey(t *testing.T) {
	ls := newTestLoopState()
	_, _ = callScratchpadWrite(map[string]any{"key": "k", "value": 42.0}, ls)
	result, isErr := callScratchpadRead(map[string]any{"key": "k"}, ls)
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	entries, _ := out["entries"].(map[string]any)
	if entries["k"] != 42.0 {
		t.Errorf("expected 42, got %v", entries["k"])
	}
}

func TestCallScratchpadRead_AllKeys(t *testing.T) {
	ls := newTestLoopState()
	_, _ = callScratchpadWrite(map[string]any{"key": "a", "value": 1.0}, ls)
	_, _ = callScratchpadWrite(map[string]any{"key": "b", "value": 2.0}, ls)
	result, isErr := callScratchpadRead(map[string]any{}, ls)
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	entries, _ := out["entries"].(map[string]any)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestCallScratchpadRead_MissingKey(t *testing.T) {
	ls := newTestLoopState()
	result, isErr := callScratchpadRead(map[string]any{"key": "nope"}, ls)
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	entries, _ := out["entries"].(map[string]any)
	if len(entries) != 0 {
		t.Errorf("expected empty entries map for missing key, got %v", entries)
	}
}

func TestCallScratchpadClear_ExistingKey(t *testing.T) {
	ls := newTestLoopState()
	_, _ = callScratchpadWrite(map[string]any{"key": "k", "value": "v"}, ls)
	result, isErr := callScratchpadClear(map[string]any{"key": "k"}, ls)
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if out["cleared"] != true {
		t.Errorf("expected cleared=true, got %v", out["cleared"])
	}
	// Verify gone.
	readResult, _ := callScratchpadRead(map[string]any{"key": "k"}, ls)
	var readOut map[string]any
	_ = json.Unmarshal([]byte(readResult), &readOut)
	entries, _ := readOut["entries"].(map[string]any)
	if len(entries) != 0 {
		t.Errorf("key still readable after clear: %v", entries)
	}
}

func TestCallScratchpadClear_MissingKey(t *testing.T) {
	ls := newTestLoopState()
	result, isErr := callScratchpadClear(map[string]any{"key": "nope"}, ls)
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(result), &out)
	if out["cleared"] != false {
		t.Errorf("expected cleared=false for missing key, got %v", out["cleared"])
	}
}

func TestCallScratchpadClear_MissingKeyArg(t *testing.T) {
	ls := newTestLoopState()
	_, isErr := callScratchpadClear(map[string]any{}, ls)
	if !isErr {
		t.Fatal("expected error when key arg is missing")
	}
}

func TestIsScratchpadTool(t *testing.T) {
	for _, name := range []string{
		"nanite_scratchpad_write",
		"nanite_scratchpad_read",
		"nanite_scratchpad_clear",
	} {
		if !isScratchpadTool(name) {
			t.Errorf("isScratchpadTool(%q) = false, want true", name)
		}
	}
	if isScratchpadTool("nanite_create_skill") {
		t.Error("isScratchpadTool(nanite_create_skill) = true, want false")
	}
}

func TestHandleScratchpadTool_ChannelEvents(t *testing.T) {
	ls := newTestLoopState()
	ch := make(chan chat.StreamEvent, 10)

	tu := provider.ToolUseBlock{
		ID:    "test-id",
		Name:  "nanite_scratchpad_write",
		Input: map[string]any{"key": "k", "value": "v"},
	}

	result := handleScratchpadTool(tu, ls, ch, nil, time.Now())
	close(ch)

	var events []chat.StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events (tool_call + tool_result), got %d", len(events))
	}
	if events[0].Type != "tool_call" {
		t.Errorf("first event type = %q, want tool_call", events[0].Type)
	}
	if events[1].Type != "tool_result" {
		t.Errorf("second event type = %q, want tool_result", events[1].Type)
	}
	if result.isError {
		t.Errorf("expected no error, got isError=true, output=%q", result.rawOutput)
	}
	if result.ref.ID != "test-id" {
		t.Errorf("expected ref.ID=test-id, got %q", result.ref.ID)
	}
}

func TestHandleScratchpadTool_SummaryTruncation(t *testing.T) {
	ls := newTestLoopState()
	// Store a value large enough that the JSON read response exceeds 500 chars.
	bigVal := strings.Repeat("x", 600)
	if err := ls.scratchpadWrite("big", bigVal); err != nil {
		t.Fatalf("setup write failed: %v", err)
	}

	ch := make(chan chat.StreamEvent, 10)
	tu := provider.ToolUseBlock{
		ID:    "read-id",
		Name:  "nanite_scratchpad_read",
		Input: map[string]any{"key": "big"},
	}

	handleScratchpadTool(tu, ls, ch, nil, time.Now())
	close(ch)

	var toolResultEvent *chat.StreamEvent
	for e := range ch {
		if e.Type == "tool_result" {
			ec := e
			toolResultEvent = &ec
		}
	}
	if toolResultEvent == nil {
		t.Fatal("no tool_result event emitted")
	}
	if len(toolResultEvent.Summary) > 520 {
		t.Errorf("summary not truncated: len=%d", len(toolResultEvent.Summary))
	}
}
