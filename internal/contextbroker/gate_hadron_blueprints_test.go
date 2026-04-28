package contextbroker

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockMCPCaller implements MCPCaller for testing. Keys in responses /
// errors are the uniform agent-facing tool name (ADR-002 — no
// `mcp__server__` prefix). Both ExecuteTool (uniform-name lookup) and
// ExecuteToolOnServer (explicit-server entry) route to the same mock
// table, since the test's intent is "did the source ask for tool X".
type mockMCPCaller struct {
	responses map[string]string
	errors    map[string]error
}

func (m *mockMCPCaller) ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error) {
	if err, exists := m.errors[name]; exists {
		return "", err
	}
	if response, exists := m.responses[name]; exists {
		return response, nil
	}
	return "", fmt.Errorf("no mock response for tool %s", name)
}

func (m *mockMCPCaller) ExecuteToolOnServer(ctx context.Context, server, tool string, input map[string]any) (string, error) {
	return m.ExecuteTool(ctx, tool, input)
}

func TestHadronBlueprintGate_Name(t *testing.T) {
	gate := NewHadronBlueprintGate(nil)
	if got := gate.Name(); got != "hadron-blueprints" {
		t.Errorf("Name() = %q, want %q", got, "hadron-blueprints")
	}
}

func TestHadronBlueprintGate_SessionStart_NoMCP(t *testing.T) {
	gate := NewHadronBlueprintGate(nil)

	intent := Intent{
		Type:  "boot_project",
		Scope: "mentat",
	}

	active, err := gate.SessionStart(context.Background(), intent)
	if err != nil {
		t.Errorf("SessionStart() with no MCP should not error, got: %v", err)
	}
	if active {
		t.Error("SessionStart() with no MCP should return false")
	}
	if gate.IsCached() {
		t.Error("Gate should not have cached data with no MCP")
	}
}

func TestHadronBlueprintGate_SessionStart_MCPError(t *testing.T) {
	mcp := &mockMCPCaller{
		errors: map[string]error{
			"hadron_blueprints_list": fmt.Errorf("connection failed"),
		},
	}
	gate := NewHadronBlueprintGate(mcp)

	intent := Intent{
		Type:  "boot_project",
		Scope: "mentat",
	}

	active, err := gate.SessionStart(context.Background(), intent)
	if err != nil {
		t.Errorf("SessionStart() should handle MCP errors gracefully, got: %v", err)
	}
	if active {
		t.Error("SessionStart() with MCP error should return false")
	}
}

func TestHadronBlueprintGate_SessionStart_Success(t *testing.T) {
	mockResponse := `{
		"items": [
			{
				"name": "build_mentat",
				"description": "Build the Mentat project",
				"tags": ["build", "mentat"]
			},
			{
				"name": "test_go_project",
				"description": "Run Go tests for any project",
				"tags": ["test", "go"]
			},
			{
				"name": "health_dashboard",
				"description": "System health monitoring dashboard",
				"tags": ["health", "monitoring"]
			}
		]
	}`

	mcp := &mockMCPCaller{
		responses: map[string]string{
			"hadron_blueprints_list": mockResponse,
		},
	}
	gate := NewHadronBlueprintGate(mcp)

	intent := Intent{
		Type:     "boot_project",
		Scope:    "mentat",
		Keywords: []string{"build", "test"},
	}

	active, err := gate.SessionStart(context.Background(), intent)
	if err != nil {
		t.Errorf("SessionStart() failed: %v", err)
	}
	if !active {
		t.Error("SessionStart() should return true when blueprints are found")
	}
	if !gate.IsCached() {
		t.Error("Gate should have cached data after successful session start")
	}
}

func TestHadronBlueprintGate_Fetch_NoCachedData(t *testing.T) {
	gate := NewHadronBlueprintGate(nil)

	intent := Intent{Type: "resume_task"}
	items, err := gate.Fetch(context.Background(), intent, 1000)

	if err != nil {
		t.Errorf("Fetch() failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Fetch() with no cached data should return empty slice, got %d items", len(items))
	}
}

func TestHadronBlueprintGate_Fetch_WithCache(t *testing.T) {
	mockResponse := `{
		"items": [
			{
				"name": "build_mentat",
				"description": "Build the Mentat project",
				"tags": ["build", "mentat"]
			},
			{
				"name": "test_go_project",
				"description": "Run Go tests for any project",
				"tags": ["test", "go"]
			}
		]
	}`

	mcp := &mockMCPCaller{
		responses: map[string]string{
			"hadron_blueprints_list": mockResponse,
		},
	}
	gate := NewHadronBlueprintGate(mcp)

	// Start session to populate cache
	intent := Intent{
		Type:     "boot_project",
		Scope:    "mentat",
		Keywords: []string{"build"},
	}

	_, err := gate.SessionStart(context.Background(), intent)
	if err != nil {
		t.Fatalf("SessionStart() failed: %v", err)
	}

	// Fetch from cache
	items, err := gate.Fetch(context.Background(), intent, 1000)
	if err != nil {
		t.Errorf("Fetch() failed: %v", err)
	}
	if len(items) == 0 {
		t.Error("Fetch() should return cached items")
	}

	// Verify first item properties
	item := items[0]
	if item.Source != "hadron-blueprints" {
		t.Errorf("Item source = %q, want %q", item.Source, "hadron-blueprints")
	}
	if item.TokenEstimate <= 0 {
		t.Error("Item should have positive token estimate")
	}
	if item.Relevance <= 0 {
		t.Error("Item should have positive relevance")
	}
}

func TestHadronBlueprintGate_ClearCache(t *testing.T) {
	gate := NewHadronBlueprintGate(nil)

	// Manually set some cache to test clearing
	gate.mutex.Lock()
	gate.cachedItems = []ContextItem{{Source: "test"}}
	gate.cacheTime = time.Now()
	gate.mutex.Unlock()

	if !gate.IsCached() {
		t.Error("Gate should have cached data before clear")
	}

	gate.ClearCache()

	if gate.IsCached() {
		t.Error("Gate should not have cached data after clear")
	}
}

func TestHadronBlueprintGate_TokenCap(t *testing.T) {
	// Create a response with many blueprints to test token capping
	items := make([]string, 20)
	for i := 0; i < 20; i++ {
		items[i] = fmt.Sprintf(`{
			"name": "blueprint_%d",
			"description": "This is a very long description for blueprint %d that should help test the token limit functionality",
			"tags": ["test"]
		}`, i, i)
	}

	itemsJSON := items[0] + "," + items[1] + "," + items[2] + "," + items[3] + "," + items[4]
	mockResponse := fmt.Sprintf(`{"items": [%s]}`, itemsJSON)

	mcp := &mockMCPCaller{
		responses: map[string]string{
			"hadron_blueprints_list": mockResponse,
		},
	}
	gate := NewHadronBlueprintGate(mcp)

	intent := Intent{Type: "boot_project"}

	_, err := gate.SessionStart(context.Background(), intent)
	if err != nil {
		t.Fatalf("SessionStart() failed: %v", err)
	}

	items_result, err := gate.Fetch(context.Background(), intent, 500)
	if err != nil {
		t.Errorf("Fetch() failed: %v", err)
	}

	// Verify token cap is respected
	totalTokens := 0
	for _, item := range items_result {
		totalTokens += item.TokenEstimate
	}

	if totalTokens > 500 {
		t.Errorf("Total tokens %d exceeds cap of 500", totalTokens)
	}
}

func TestHadronBlueprintGate_RelevanceFiltering(t *testing.T) {
	mockResponse := `{
		"items": [
			{
				"name": "build_mentat",
				"description": "Build the Mentat project",
				"tags": ["build", "mentat"]
			},
			{
				"name": "suds_game_action",
				"description": "SUDS game specific action",
				"tags": ["game", "suds"]
			},
			{
				"name": "health_check",
				"description": "System health monitoring",
				"tags": ["health", "monitoring"]
			}
		]
	}`

	mcp := &mockMCPCaller{
		responses: map[string]string{
			"hadron_blueprints_list": mockResponse,
		},
	}
	gate := NewHadronBlueprintGate(mcp)

	intent := Intent{
		Type:  "boot_project",
		Scope: "mentat",
	}

	_, err := gate.SessionStart(context.Background(), intent)
	if err != nil {
		t.Fatalf("SessionStart() failed: %v", err)
	}

	items, err := gate.Fetch(context.Background(), intent, 1000)
	if err != nil {
		t.Errorf("Fetch() failed: %v", err)
	}

	// Should include mentat-specific and health blueprints, exclude SUDS game
	if len(items) == 0 {
		t.Error("Should have found relevant blueprints")
	}

	// Verify the item contains expected blueprints in content
	content := items[0].Content
	if !contains(content, "build_mentat") {
		t.Error("Should include project-specific blueprint")
	}
	if !contains(content, "health_check") {
		t.Error("Should include essential cross-cutting blueprint")
	}
	// SUDS should have low relevance and likely be filtered out
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		   (len(s) >= len(substr)) &&
		   (s == substr || len(s) > len(substr) &&
		   (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		   containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}