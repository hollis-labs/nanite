package chat

import (
	"context"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantArgs string
	}{
		{"/help", "help", ""},
		{"/agent claude-sonnet", "agent", "claude-sonnet"},
		{"/search hello world", "search", "hello world"},
		{"help", "help", ""},    // without leading slash
		{"/model", "model", ""}, // no args
		{"/export  extra spaces ", "export", "extra spaces"},
	}

	for _, tt := range tests {
		name, args := ParseCommand(tt.input)
		if name != tt.wantName {
			t.Errorf("ParseCommand(%q) name = %q, want %q", tt.input, name, tt.wantName)
		}
		if args != tt.wantArgs {
			t.Errorf("ParseCommand(%q) args = %q, want %q", tt.input, args, tt.wantArgs)
		}
	}
}

func TestCommandRegistry_ListAndExecute(t *testing.T) {
	r := NewCommandRegistry()

	// List should return built-in commands.
	cmds := r.List()
	if len(cmds) == 0 {
		t.Fatal("expected at least one built-in command")
	}

	// Verify /help is present.
	found := false
	for _, c := range cmds {
		if c.Name == "help" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'help' command in list")
	}
}

func TestCommandRegistry_ExecuteHelp(t *testing.T) {
	r := NewCommandRegistry()

	result, err := r.Execute(context.Background(), "help", "sess-1", "")
	if err != nil {
		t.Fatalf("Execute(help) error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for help")
	}
	if result.Action != "message" {
		t.Errorf("expected action 'message', got %q", result.Action)
	}
	if !strings.Contains(result.Content, "Available Commands") {
		t.Error("expected 'Available Commands' in help output")
	}
}

func TestCommandRegistry_ExecuteClientSideCommand(t *testing.T) {
	r := NewCommandRegistry()

	// /new is client-side (nil handler).
	result, err := r.Execute(context.Background(), "new", "sess-1", "")
	if err != nil {
		t.Fatalf("Execute(new) error: %v", err)
	}
	if result.Action != "client" {
		t.Errorf("expected action 'client', got %q", result.Action)
	}
}

func TestCommandRegistry_ExecuteUnknown(t *testing.T) {
	r := NewCommandRegistry()

	_, err := r.Execute(context.Background(), "nonexistent", "sess-1", "")
	if err == nil {
		t.Error("expected error for unknown command")
	}
}

func TestCommandRegistry_Register(t *testing.T) {
	r := NewCommandRegistry()

	r.Register(SlashCommand{
		Name:        "custom",
		Description: "A custom command",
		Category:    "test",
		Source:      "test",
	}, func(_ context.Context, sessionID, args string) (*CommandResult, error) {
		return &CommandResult{Action: "message", Content: "custom: " + args}, nil
	})

	result, err := r.Execute(context.Background(), "custom", "s1", "hello")
	if err != nil {
		t.Fatalf("Execute(custom) error: %v", err)
	}
	if result.Content != "custom: hello" {
		t.Errorf("expected 'custom: hello', got %q", result.Content)
	}

	// Verify it appears in list.
	found := false
	for _, c := range r.List() {
		if c.Name == "custom" {
			found = true
			if c.Category != "test" {
				t.Errorf("expected category 'test', got %q", c.Category)
			}
		}
	}
	if !found {
		t.Error("custom command not in list")
	}
}
