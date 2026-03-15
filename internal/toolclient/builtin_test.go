package toolclient

import (
	"testing"

	"github.com/hollis-labs/conduit/internal/provider"
)

func TestRegisterBuiltins(t *testing.T) {
	reg := NewBuiltinToolRegistry()

	devTools := []provider.ToolDefinition{
		{Name: "dev_read", Description: "Read file contents"},
		{Name: "dev_write", Description: "Write file contents"},
		{Name: "dev_grep", Description: "Search files"},
	}
	generalTools := []provider.ToolDefinition{
		{Name: "web_fetch", Description: "Fetch a URL"},
		{Name: "datetime", Description: "Get current time"},
	}

	reg.RegisterBuiltins("dev", devTools)
	reg.RegisterBuiltins("general", generalTools)

	if reg.Count() != 5 {
		t.Errorf("expected 5 total tools, got %d", reg.Count())
	}

	devResult := reg.GetBuiltinsByCategory("dev")
	if len(devResult) != 3 {
		t.Errorf("expected 3 dev tools, got %d", len(devResult))
	}

	generalResult := reg.GetBuiltinsByCategory("general")
	if len(generalResult) != 2 {
		t.Errorf("expected 2 general tools, got %d", len(generalResult))
	}
}

func TestGetBuiltins_AlwaysAvailable(t *testing.T) {
	reg := NewBuiltinToolRegistry()

	// Even with no MCP servers, builtins are available after registration.
	devTools := []provider.ToolDefinition{
		{Name: "dev_read", Description: "Read file contents"},
		{Name: "dev_bash", Description: "Execute shell command"},
	}
	generalTools := []provider.ToolDefinition{
		{Name: "datetime", Description: "Get current time"},
	}

	reg.RegisterBuiltins("dev", devTools)
	reg.RegisterBuiltins("general", generalTools)

	all := reg.GetBuiltins()
	if len(all) != 3 {
		t.Errorf("expected 3 total built-in tools, got %d", len(all))
	}

	// Verify all tools are present by name.
	names := make(map[string]bool)
	for _, tool := range all {
		names[tool.Name] = true
	}
	for _, expected := range []string{"dev_read", "dev_bash", "datetime"} {
		if !names[expected] {
			t.Errorf("expected built-in tool %q to be present", expected)
		}
	}
}

func TestGetBuiltinsByCategory(t *testing.T) {
	reg := NewBuiltinToolRegistry()

	reg.RegisterBuiltins("dev", []provider.ToolDefinition{
		{Name: "dev_read", Description: "Read file"},
	})
	reg.RegisterBuiltins("general", []provider.ToolDefinition{
		{Name: "web_fetch", Description: "Fetch URL"},
	})

	devTools := reg.GetBuiltinsByCategory("dev")
	if len(devTools) != 1 || devTools[0].Name != "dev_read" {
		t.Errorf("expected dev_read, got %v", devTools)
	}

	generalTools := reg.GetBuiltinsByCategory("general")
	if len(generalTools) != 1 || generalTools[0].Name != "web_fetch" {
		t.Errorf("expected web_fetch, got %v", generalTools)
	}

	// Unknown category returns nil.
	unknown := reg.GetBuiltinsByCategory("nonexistent")
	if unknown != nil {
		t.Errorf("expected nil for unknown category, got %v", unknown)
	}
}

func TestGetBuiltins_EmptyRegistry(t *testing.T) {
	reg := NewBuiltinToolRegistry()

	all := reg.GetBuiltins()
	if len(all) != 0 {
		t.Errorf("expected 0 tools from empty registry, got %d", len(all))
	}
	if reg.Count() != 0 {
		t.Errorf("expected count 0, got %d", reg.Count())
	}
}

func TestRegisterBuiltins_OverwritesCategory(t *testing.T) {
	reg := NewBuiltinToolRegistry()

	reg.RegisterBuiltins("dev", []provider.ToolDefinition{
		{Name: "dev_read", Description: "Read file"},
	})
	if reg.Count() != 1 {
		t.Fatalf("expected 1 tool, got %d", reg.Count())
	}

	// Re-register overwrites the category.
	reg.RegisterBuiltins("dev", []provider.ToolDefinition{
		{Name: "dev_read", Description: "Read file"},
		{Name: "dev_write", Description: "Write file"},
	})
	if reg.Count() != 2 {
		t.Errorf("expected 2 tools after re-register, got %d", reg.Count())
	}
}
