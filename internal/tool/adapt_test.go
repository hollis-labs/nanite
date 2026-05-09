package tool_test

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/tool"
)

func TestWrapProviderDef(t *testing.T) {
	def := llmtypes.ToolDefinition{
		Name:        "dev_read",
		Description: "Read a file",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
	}

	wrapped := tool.WrapProviderDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, []string{"dev", "io"})

	if wrapped.Name() != "dev_read" {
		t.Errorf("Name() = %q", wrapped.Name())
	}
	if wrapped.Category() != tool.CategoryCoreIO {
		t.Errorf("Category() = %q", wrapped.Category())
	}
	if wrapped.Source() != tool.SourceBuiltin {
		t.Errorf("Source() = %q", wrapped.Source())
	}
	// dev_read should be inferred as read-only + concurrency-safe.
	if !wrapped.IsReadOnly(nil) {
		t.Error("dev_read should be read-only")
	}
	if !wrapped.IsConcurrencySafe(nil) {
		t.Error("dev_read should be concurrency-safe")
	}
}

func TestToProviderDefinition_Roundtrip(t *testing.T) {
	original := llmtypes.ToolDefinition{
		Name:        "web_fetch",
		Description: "Fetch a URL",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string"},
			},
		},
	}

	wrapped := tool.WrapProviderDef(original, tool.CategorySearch, tool.SourceBuiltin, nil)
	roundtripped := tool.ToProviderDefinition(wrapped)

	if roundtripped.Name != original.Name {
		t.Errorf("Name = %q", roundtripped.Name)
	}
	if roundtripped.Description != original.Description {
		t.Errorf("Description = %q", roundtripped.Description)
	}
	if roundtripped.InputSchema["type"] != "object" {
		t.Errorf("InputSchema type = %v", roundtripped.InputSchema["type"])
	}
}

func TestToProviderDefinitions(t *testing.T) {
	tools := []tool.Tool{
		tool.NewTool("a", "tool a"),
		tool.NewTool("b", "tool b"),
	}
	defs := tool.ToProviderDefinitions(tools)
	if len(defs) != 2 {
		t.Errorf("got %d defs, want 2", len(defs))
	}
	if defs[0].Name != "a" || defs[1].Name != "b" {
		t.Errorf("names = %q, %q", defs[0].Name, defs[1].Name)
	}
}

func TestWrapExistingTools(t *testing.T) {
	defs := []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "read"},
		{Name: "dev_write", Description: "write"},
		{Name: "web_fetch", Description: "fetch"},
		{Name: "agent_list", Description: "list agents"},
		{Name: "unknown_tool", Description: "mystery"},
	}

	tools := tool.WrapExistingTools(defs)
	if len(tools) != 5 {
		t.Fatalf("got %d tools, want 5", len(tools))
	}

	// dev_read → core-io
	if tools[0].Category() != tool.CategoryCoreIO {
		t.Errorf("dev_read category = %q", tools[0].Category())
	}
	// web_fetch → search
	if tools[2].Category() != tool.CategorySearch {
		t.Errorf("web_fetch category = %q", tools[2].Category())
	}
	// agent_list → agent
	if tools[3].Category() != tool.CategoryAgent {
		t.Errorf("agent_list category = %q", tools[3].Category())
	}
	// Unknown tool defaults to session/builtin (post ADR-002 there is no
	// `mcp__` substring signal — callers wrap MCP-origin tools explicitly
	// via WrapProviderDef + SourceMCP).
	if tools[4].Category() != tool.CategorySession {
		t.Errorf("unknown tool category = %q, want %q", tools[4].Category(), tool.CategorySession)
	}
	if tools[4].Source() != tool.SourceBuiltin {
		t.Errorf("unknown tool source = %q, want %q", tools[4].Source(), tool.SourceBuiltin)
	}
}

func TestBashSafety(t *testing.T) {
	def := llmtypes.ToolDefinition{
		Name:        "dev_bash",
		Description: "shell",
		InputSchema: map[string]any{"type": "object"},
	}
	wrapped := tool.WrapProviderDef(def, tool.CategoryCoreIO, tool.SourceBuiltin, nil)

	// ls should be read-only and safe.
	lsInput := map[string]any{"command": "ls -la"}
	if !wrapped.IsReadOnly(lsInput) {
		t.Error("ls should be read-only")
	}
	if !wrapped.IsConcurrencySafe(lsInput) {
		t.Error("ls should be concurrency-safe")
	}

	// rm should be destructive and not safe.
	rmInput := map[string]any{"command": "rm -rf /tmp/foo"}
	if wrapped.IsReadOnly(rmInput) {
		t.Error("rm should not be read-only")
	}
	if !wrapped.IsDestructive(rmInput) {
		t.Error("rm should be destructive")
	}
	if wrapped.IsConcurrencySafe(rmInput) {
		t.Error("rm should not be concurrency-safe")
	}
}
