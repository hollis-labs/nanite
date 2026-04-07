package agentvalidation

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestValidAgent(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "Test Agent",
		Slug:            "test-agent",
		SystemPrompt:    "You are a test agent.",
		MCPServers:      `["conduit"]`,
		ToolPermissions: `{"allow_list":["mcp__conduit__*"]}`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors, got: %v", result.Errors)
	}
	if len(result.Warnings) > 0 {
		t.Fatalf("expected no warnings, got: %v", result.Warnings)
	}
}

func TestMalformedToolPermissionsJSON(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "Bad Agent",
		Slug:            "bad-agent",
		SystemPrompt:    "You are broken.",
		ToolPermissions: `{"allow_list": [}`,
	}
	result := ValidateAgentConfig(agent)
	if result.OK() {
		t.Fatal("expected errors for malformed JSON, got none")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestEmptyAllowList(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "Empty Allow",
		Slug:            "empty-allow",
		SystemPrompt:    "You are restricted.",
		MCPServers:      `["conduit"]`,
		ToolPermissions: `{"allow_list":[]}`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors, got: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warning about empty allow_list, got none")
	}
	found := false
	for _, w := range result.Warnings {
		if w == "allow_list is empty - this will deny all tools" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty allow_list warning, got: %v", result.Warnings)
	}
}

func TestNoMCPServersPermissivePermissions(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "No Tools",
		Slug:            "no-tools",
		SystemPrompt:    "You have nothing.",
		MCPServers:      `[]`,
		ToolPermissions: `{}`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors, got: %v", result.Errors)
	}
	found := false
	for _, w := range result.Warnings {
		if w == "agent has no MCP servers and permissive permissions - it will have no tools but unrestricted access" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected MCP + permissive warning, got: %v", result.Warnings)
	}
}

func TestInvalidGlobPattern(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "Bad Glob",
		Slug:            "bad-glob",
		SystemPrompt:    "You have bad patterns.",
		MCPServers:      `["conduit"]`,
		ToolPermissions: `{"allow_list":["mcp__conduit__[invalid"]}`,
	}
	result := ValidateAgentConfig(agent)
	if result.OK() {
		t.Fatal("expected errors for invalid glob pattern, got none")
	}
}

func TestValidAgentWithAllowAndMCPServers(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "Full Agent",
		Slug:            "full-agent",
		SystemPrompt:    "You are a full agent.",
		MCPServers:      `["conduit","engine"]`,
		ToolPermissions: `{"allow_list":["mcp__conduit__*","mcp__engine__*"],"max_calls_per_turn":10}`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors, got: %v", result.Errors)
	}
	if len(result.Warnings) > 0 {
		t.Fatalf("expected no warnings, got: %v", result.Warnings)
	}
}

func TestEmptyPermissionsWithMCPServers(t *testing.T) {
	// Agent with MCP servers but empty permissions — no warning expected
	// (this is a valid permissive config — they have tools available)
	agent := &store.AgentProfile{
		Name:            "Permissive Agent",
		Slug:            "permissive",
		SystemPrompt:    "You are permissive.",
		MCPServers:      `["conduit"]`,
		ToolPermissions: `{}`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors, got: %v", result.Errors)
	}
	// Should NOT warn — they have MCP servers
	for _, w := range result.Warnings {
		if w == "agent has no MCP servers and permissive permissions - it will have no tools but unrestricted access" {
			t.Fatal("should not warn about permissive when MCP servers are present")
		}
	}
}

func TestDenyListWithInvalidGlob(t *testing.T) {
	agent := &store.AgentProfile{
		Name:            "Bad Deny",
		Slug:            "bad-deny",
		SystemPrompt:    "You have bad deny.",
		MCPServers:      `["conduit"]`,
		ToolPermissions: `{"deny_list":["mcp__[bad"]}`,
	}
	result := ValidateAgentConfig(agent)
	if result.OK() {
		t.Fatal("expected errors for invalid glob in deny_list, got none")
	}
}

func TestPrefixGlobIsValid(t *testing.T) {
	// Patterns ending in * are prefix globs and should always be valid
	agent := &store.AgentProfile{
		Name:            "Prefix Glob",
		Slug:            "prefix-glob",
		SystemPrompt:    "You use prefix globs.",
		MCPServers:      `["conduit"]`,
		ToolPermissions: `{"allow_list":["mcp__conduit__*","mcp__engine__task_*"]}`,
	}
	result := ValidateAgentConfig(agent)
	if !result.OK() {
		t.Fatalf("expected no errors for prefix globs, got: %v", result.Errors)
	}
}
