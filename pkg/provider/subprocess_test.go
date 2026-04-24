package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubprocessBridge_Capabilities(t *testing.T) {
	bridge := NewSubprocessBridge(NewClaudeAdapter(), "/usr/bin/echo")
	caps := bridge.Capabilities()

	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON=true")
	}
	if !caps.SupportsToolCalling {
		t.Error("expected SupportsToolCalling=true")
	}
	if caps.SupportsSystemPromptCaching {
		t.Error("expected SupportsSystemPromptCaching=false")
	}
	if caps.SupportsBatch {
		t.Error("expected SupportsBatch=false")
	}
}

func TestSubprocessBridge_StreamChat_NoUserMessage(t *testing.T) {
	bridge := NewSubprocessBridge(NewClaudeAdapter(), "/usr/bin/echo")
	_, err := bridge.StreamChat(context.Background(), "", nil, "")
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestSubprocessBridge_StreamChat_SystemOnly(t *testing.T) {
	bridge := NewSubprocessBridge(NewClaudeAdapter(), "/usr/bin/echo")
	_, err := bridge.StreamChat(context.Background(), "", []ChatMessage{
		{Role: "system", Content: "test"},
	}, "")
	if err == nil {
		t.Fatal("expected error for no user message")
	}
}

func TestSubprocessBridge_Complete_MockCLI(t *testing.T) {
	// Create a mock CLI script that outputs stream-json events.
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from subprocess!"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Hello from subprocess!","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	result, err := bridge.Complete(context.Background(), "", []ChatMessage{
		{Role: "user", Content: "test prompt"},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello from subprocess!") {
		t.Errorf("expected 'Hello from subprocess!' in result, got: %s", result)
	}
}

func TestSubprocessBridge_StreamChat_MockCLI(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"system","subtype":"init","cwd":"/tmp","session_id":"sess-abc"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"delta one"}]}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"delta two"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), "", []ChatMessage{
		{Role: "user", Content: "test"},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Expect: session_id, delta, delta, usage, done
	hasSessionID := false
	deltaCount := 0
	hasDone := false
	for _, ev := range events {
		switch ev.Type {
		case "session_id":
			hasSessionID = true
		case "delta":
			deltaCount++
		case "done":
			hasDone = true
		}
	}

	if !hasSessionID {
		t.Error("expected session_id event")
	}
	if deltaCount != 2 {
		t.Errorf("expected 2 delta events, got %d", deltaCount)
	}
	if !hasDone {
		t.Error("expected done event")
	}
}

func TestSubprocessBridge_SandboxDir(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-cli.sh")
	// Script outputs the working directory as a delta so we can verify sandbox dir was set.
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo "{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"$(pwd)\"}]}}"
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	sandboxDir := t.TempDir()
	ctx := WithSandboxDir(context.Background(), sandboxDir)

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	result, err := bridge.Complete(ctx, "", []ChatMessage{
		{Role: "user", Content: "test"},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, sandboxDir) {
		t.Errorf("expected sandbox dir %s in result, got: %s", sandboxDir, result)
	}
}

// TestSubprocessBridge_ToolOnlyResponse_EmitsError verifies that when the CLI
// produces only tool_use events (and no text deltas), the subprocess bridge
// emits an error event before closing the channel — instead of silently closing
// with zero content. This is the fix for the PTY tool-call passthrough gap:
// the nested CLI requested tools that Nanite's bridge cannot forward.
func TestSubprocessBridge_ToolOnlyResponse_EmitsError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tool-only-cli.sh")
	// Script outputs an assistant message with only tool_use blocks and a
	// successful result — no text content at all.
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"system","subtype":"init","cwd":"/tmp","session_id":"sess-tool"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), "", []ChatMessage{
		{Role: "user", Content: "list files"},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Must have at least one error event.
	var errorEvents []StreamEvent
	for _, ev := range events {
		if ev.Type == "error" {
			errorEvents = append(errorEvents, ev)
		}
	}
	if len(errorEvents) == 0 {
		t.Errorf("expected at least one error event for tool-only response, got none; all events: %v", events)
	}
	// Error message must mention tool forwarding.
	if len(errorEvents) > 0 && !strings.Contains(errorEvents[0].Error, "tool calls") {
		t.Errorf("expected error about tool calls, got: %q", errorEvents[0].Error)
	}
}

// TestSubprocessBridge_MixedResponse_NoSpuriousError verifies that a response
// with both text and tool_use blocks does NOT trigger the tool-proxy error.
// (The CLI processed its own tools and returned a textual reply — that's fine.)
func TestSubprocessBridge_MixedResponse_NoSpuriousError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mixed-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Here is the result:"},{"type":"tool_use","id":"tu_2","name":"Read","input":{"file_path":"/tmp/x"}}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Here is the result:","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), "", []ChatMessage{
		{Role: "user", Content: "read a file"},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Must have a delta event (text content).
	hasDelta := false
	for _, ev := range events {
		if ev.Type == "delta" {
			hasDelta = true
		}
	}
	if !hasDelta {
		t.Error("expected at least one delta event for mixed response")
	}

	// Must NOT have a spurious tool-proxy error event.
	for _, ev := range events {
		if ev.Type == "error" && strings.Contains(ev.Error, "tool calls") {
			t.Errorf("spurious tool-proxy error emitted for mixed-content response: %q", ev.Error)
		}
	}
}

// TestSubprocessBridge_PureTextResponse_NoError verifies that a pure text
// response does not trigger the tool-proxy error event.
func TestSubprocessBridge_PureTextResponse_NoError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "text-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Just a plain text reply."}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Just a plain text reply.","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), "", []ChatMessage{
		{Role: "user", Content: "hello"},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	for _, ev := range events {
		if ev.Type == "error" {
			t.Errorf("unexpected error event for pure-text response: %q", ev.Error)
		}
	}
}

func TestSubprocessBridge_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "slow-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"start"}]}}'
sleep 30
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(ctx, "", []ChatMessage{
		{Role: "user", Content: "test"},
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read the first event, then cancel.
	ev := <-ch
	if ev.Type != "delta" {
		t.Errorf("expected first event to be delta, got %s", ev.Type)
	}
	cancel()

	// Drain remaining events — should get error or channel close.
	for ev := range ch {
		_ = ev
	}
	// If we get here without hanging, the test passed.
}
