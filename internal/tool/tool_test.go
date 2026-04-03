package tool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/conduit/internal/tool"
)

func TestNewTool_Defaults(t *testing.T) {
	tt := tool.NewTool("test-tool", "A test tool")

	if tt.Name() != "test-tool" {
		t.Errorf("Name() = %q, want %q", tt.Name(), "test-tool")
	}
	if tt.Description() != "A test tool" {
		t.Errorf("Description() = %q, want %q", tt.Description(), "A test tool")
	}
	if tt.Category() != "" {
		t.Errorf("Category() = %q, want empty", tt.Category())
	}
	if tt.Source() != tool.SourceBuiltin {
		t.Errorf("Source() = %q, want %q", tt.Source(), tool.SourceBuiltin)
	}
	if tt.Timeout() != 0 {
		t.Errorf("Timeout() = %v, want 0", tt.Timeout())
	}
	if len(tt.Tags()) != 0 {
		t.Errorf("Tags() = %v, want empty", tt.Tags())
	}
	if len(tt.DefaultPermissions()) != 0 {
		t.Errorf("DefaultPermissions() = %v, want empty", tt.DefaultPermissions())
	}

	// Default schema is a bare object.
	var schema map[string]any
	if err := json.Unmarshal(tt.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema() not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("InputSchema() type = %v, want object", schema["type"])
	}

	// Default safety: not concurrent-safe, not read-only, not destructive.
	input := map[string]any{}
	if tt.IsConcurrencySafe(input) {
		t.Error("IsConcurrencySafe() = true, want false")
	}
	if tt.IsReadOnly(input) {
		t.Error("IsReadOnly() = true, want false")
	}
	if tt.IsDestructive(input) {
		t.Error("IsDestructive() = true, want false")
	}

	// ValidateInput with no validator returns nil.
	if err := tt.ValidateInput(input); err != nil {
		t.Errorf("ValidateInput() = %v, want nil", err)
	}

	// Call with no callFn returns error.
	_, err := tt.Call(context.Background(), input, tool.ExecutionContext{})
	if err == nil {
		t.Error("Call() with no callFn should return error")
	}
}

func TestNewTool_WithOptions(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}

	called := false
	tt := tool.NewTool("read-file", "Read a file",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithSource(tool.SourceBuiltin),
		tool.WithSchemaMap(schema),
		tool.WithTags("io", "file"),
		tool.WithTimeout(30*time.Second),
		tool.WithReadOnly(true),
		tool.WithConcurrencySafe(true),
		tool.WithDestructive(false),
		tool.WithPermissions(tool.PermissionRule{Pattern: "/src/**", Behavior: "allow"}),
		tool.WithValidateFunc(func(input map[string]any) error {
			if _, ok := input["path"]; !ok {
				return fmt.Errorf("path is required")
			}
			return nil
		}),
		tool.WithCallFunc(func(_ context.Context, input map[string]any, _ tool.ExecutionContext) (*tool.ToolResult, error) {
			called = true
			return &tool.ToolResult{Output: "file contents"}, nil
		}),
	)

	if tt.Category() != tool.CategoryCoreIO {
		t.Errorf("Category() = %q, want %q", tt.Category(), tool.CategoryCoreIO)
	}
	if tt.Source() != tool.SourceBuiltin {
		t.Errorf("Source() = %q, want %q", tt.Source(), tool.SourceBuiltin)
	}
	if tt.Timeout() != 30*time.Second {
		t.Errorf("Timeout() = %v, want 30s", tt.Timeout())
	}

	tags := tt.Tags()
	if len(tags) != 2 || tags[0] != "io" || tags[1] != "file" {
		t.Errorf("Tags() = %v, want [io file]", tags)
	}

	perms := tt.DefaultPermissions()
	if len(perms) != 1 || perms[0].Pattern != "/src/**" || perms[0].Behavior != "allow" {
		t.Errorf("DefaultPermissions() = %v, unexpected", perms)
	}

	input := map[string]any{"path": "/foo/bar.go"}
	if !tt.IsReadOnly(input) {
		t.Error("IsReadOnly() = false, want true")
	}
	if !tt.IsConcurrencySafe(input) {
		t.Error("IsConcurrencySafe() = false, want true")
	}
	if tt.IsDestructive(input) {
		t.Error("IsDestructive() = true, want false")
	}

	// ValidateInput succeeds with path present.
	if err := tt.ValidateInput(input); err != nil {
		t.Errorf("ValidateInput() = %v, want nil", err)
	}
	// ValidateInput fails without path.
	if err := tt.ValidateInput(map[string]any{}); err == nil {
		t.Error("ValidateInput() with missing path should fail")
	}

	// Call succeeds.
	result, err := tt.Call(context.Background(), input, tool.ExecutionContext{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if !called {
		t.Error("Call() did not invoke callFn")
	}
	if result.Output != "file contents" {
		t.Errorf("Call() output = %q, want %q", result.Output, "file contents")
	}
	if result.IsError {
		t.Error("Call() result.IsError = true, want false")
	}
}

func TestNewTool_InputDependentFuncs(t *testing.T) {
	// Simulate a shell tool where safety depends on the command.
	tt := tool.NewTool("shell", "Execute a shell command",
		tool.WithCategory(tool.CategoryCoreIO),
		tool.WithConcurrencySafeFunc(func(input map[string]any) bool {
			cmd, _ := input["command"].(string)
			return cmd == "ls" || cmd == "pwd"
		}),
		tool.WithReadOnlyFunc(func(input map[string]any) bool {
			cmd, _ := input["command"].(string)
			return cmd == "ls" || cmd == "cat"
		}),
		tool.WithDestructiveFunc(func(input map[string]any) bool {
			cmd, _ := input["command"].(string)
			return cmd == "rm" || cmd == "rm -rf"
		}),
	)

	safe := map[string]any{"command": "ls"}
	dangerous := map[string]any{"command": "rm"}

	if !tt.IsConcurrencySafe(safe) {
		t.Error("ls should be concurrency-safe")
	}
	if tt.IsConcurrencySafe(dangerous) {
		t.Error("rm should not be concurrency-safe")
	}
	if !tt.IsReadOnly(safe) {
		t.Error("ls should be read-only")
	}
	if tt.IsReadOnly(dangerous) {
		t.Error("rm should not be read-only")
	}
	if tt.IsDestructive(safe) {
		t.Error("ls should not be destructive")
	}
	if !tt.IsDestructive(dangerous) {
		t.Error("rm should be destructive")
	}
}

func TestNewTool_WithSchemaRawMessage(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)
	tt := tool.NewTool("fetch", "Fetch URL", tool.WithSchema(raw))

	if string(tt.InputSchema()) != string(raw) {
		t.Errorf("InputSchema() = %s, want %s", tt.InputSchema(), raw)
	}
}

func TestToolResult_Fields(t *testing.T) {
	r := &tool.ToolResult{
		Output:       "result data",
		IsError:      false,
		Metadata:     map[string]string{"tokens": "42"},
		EnvelopeType: "kb-result",
	}
	if r.Output != "result data" {
		t.Errorf("Output = %q", r.Output)
	}
	if r.EnvelopeType != "kb-result" {
		t.Errorf("EnvelopeType = %q", r.EnvelopeType)
	}
	if r.Metadata["tokens"] != "42" {
		t.Errorf("Metadata[tokens] = %q", r.Metadata["tokens"])
	}
}

func TestConstants(t *testing.T) {
	// Verify category constants match the decisions doc.
	categories := []string{
		tool.CategoryCoreIO, tool.CategorySearch, tool.CategoryMCP,
		tool.CategoryAgent, tool.CategorySession, tool.CategoryContext,
		tool.CategoryMode,
	}
	expected := []string{"core-io", "search", "mcp", "agent", "session", "context", "mode"}
	for i, c := range categories {
		if c != expected[i] {
			t.Errorf("category %d = %q, want %q", i, c, expected[i])
		}
	}

	// Verify source constants.
	sources := []string{
		tool.SourceBuiltin, tool.SourceMCP, tool.SourcePlugin,
		tool.SourceUser, tool.SourceYAML,
	}
	expectedSrc := []string{"builtin", "mcp", "plugin", "user", "yaml"}
	for i, s := range sources {
		if s != expectedSrc[i] {
			t.Errorf("source %d = %q, want %q", i, s, expectedSrc[i])
		}
	}
}
