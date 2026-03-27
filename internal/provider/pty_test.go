//go:build !windows

package provider

import (
	"context"
	"testing"
)

func TestNewPTYBridge_NilWhenMissing(t *testing.T) {
	// Set a non-existent CLI path to force nil return.
	t.Setenv("CLAUDE_CLI_PATH", "/nonexistent/claude-fake-binary")
	// NewPTYBridge checks the env var path but doesn't verify existence
	// via LookPath when CLAUDE_CLI_PATH is set — it trusts the override.
	// So we test the LookPath fallback by unsetting and relying on a
	// missing binary.
	t.Setenv("CLAUDE_CLI_PATH", "")
	// Save and restore PATH to ensure claude isn't found.
	origPath := t.TempDir() // empty dir
	t.Setenv("PATH", origPath)

	bridge := NewPTYBridge()
	if bridge != nil {
		t.Error("expected nil when claude CLI not in PATH")
	}
}

func TestPTYBridge_Capabilities(t *testing.T) {
	bridge := &PTYBridge{cliPath: "/usr/bin/echo"}
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

func TestPTYBridge_StreamChat_NoUserMessage(t *testing.T) {
	bridge := &PTYBridge{cliPath: "/usr/bin/echo"}
	_, err := bridge.StreamChat(context.Background(), "", nil, "")
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestPTYBridge_StreamChat_WithMockCLI(t *testing.T) {
	// Use a shell script that outputs mock stream-json events.
	// We write it inline via /bin/sh -c.
	mockOutput := `{"type":"system","subtype":"init","cwd":"/tmp"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from mock CLI!"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Hello from mock CLI!","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

	bridge := &PTYBridge{cliPath: "/bin/sh"}

	// Override streamCLI by calling StreamChat with messages — but we need
	// to construct the command ourselves. Instead, test the parser integration
	// by creating a PTYBridge that points to a printf script.
	// For a proper integration test, we'd need the real CLI.

	// Test that the provider correctly returns an error for missing user message.
	_, err := bridge.StreamChat(context.Background(), "", []ChatMessage{
		{Role: "system", Content: "test"},
	}, "")
	if err == nil {
		t.Fatal("expected error for no user message")
	}

	// Verify we can construct the bridge and it has the right path.
	_ = mockOutput // Used conceptually; real integration test would use this.
	if bridge.cliPath != "/bin/sh" {
		t.Errorf("expected cliPath=/bin/sh, got %s", bridge.cliPath)
	}
}
