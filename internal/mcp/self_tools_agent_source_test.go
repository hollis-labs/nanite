package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestAgentSourceResolve_RoundTrip is the C1 resolver-endpoint check
// (S5 platform-reshape, locked decision D2). It seeds an agent into the
// agent_profiles SoT, calls agent_source_resolve through the self-tools
// transport, and asserts the composed agent comes back in an
// agentlaunch-compatible shape.
func TestAgentSourceResolve_RoundTrip(t *testing.T) {
	st := newSelfTools(t)

	seed := &store.AgentProfile{
		Name:         "Recon Agent",
		Slug:         "recon-agent",
		SystemPrompt: "You are the recon agent.",
		Description:  "scouts the codebase",
		DefaultModel: "claude-sonnet",
	}
	if err := st.Store.CreateAgent(seed); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// Resolve by slug — the directory registers an agent-source under
	// its slug, so slug is the resolution key a consumer holds.
	res, err := st.CallTool(t.Context(), agentSourceResolveToolName, map[string]any{
		"agent": "recon-agent",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("agent_source_resolve returned error: %s", res.Content[0].Text)
	}

	var out agentSourceResolveResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal resolve result: %v\nbody: %s", err, res.Content[0].Text)
	}

	if out.SystemPrompt != seed.SystemPrompt {
		t.Errorf("SystemPrompt = %q, want %q", out.SystemPrompt, seed.SystemPrompt)
	}
	if out.Slug != "recon-agent" {
		t.Errorf("Slug = %q, want recon-agent", out.Slug)
	}
	// The agentlaunch.AgentSpec-equivalent projection must carry the
	// slug as the stable id and the display name.
	if out.AgentSpec.ID != "recon-agent" {
		t.Errorf("AgentSpec.ID = %q, want recon-agent", out.AgentSpec.ID)
	}
	if out.AgentSpec.Name != "Recon Agent" {
		t.Errorf("AgentSpec.Name = %q, want Recon Agent", out.AgentSpec.Name)
	}
}

// TestAgentSourceResolve_ResolvesByID confirms an id also resolves (the
// fallback path when a caller holds the row id rather than the slug).
func TestAgentSourceResolve_ResolvesByID(t *testing.T) {
	st := newSelfTools(t)
	seed := &store.AgentProfile{
		Name:         "By ID Agent",
		Slug:         "by-id-agent",
		SystemPrompt: "resolve me by id",
	}
	if err := st.Store.CreateAgent(seed); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	res, err := st.CallTool(t.Context(), agentSourceResolveToolName, map[string]any{
		"agent": seed.ID,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("resolve by id returned error: %s", res.Content[0].Text)
	}
	var out agentSourceResolveResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != seed.ID {
		t.Errorf("ID = %q, want %q", out.ID, seed.ID)
	}
}

// TestAgentSourceResolve_NotFound confirms an unknown ref yields a clean
// tool-level error rather than a transport failure.
func TestAgentSourceResolve_NotFound(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(t.Context(), agentSourceResolveToolName, map[string]any{
		"agent": "no-such-agent",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool-level error for an unknown agent")
	}
	if !strings.Contains(res.Content[0].Text, "no agent profile found") {
		t.Errorf("error text = %q, want a not-found message", res.Content[0].Text)
	}
}

// TestAgentSourceResolve_Registered confirms the resolver tool is in the
// self-tool definitions list so it is discoverable as an MCP tool.
func TestAgentSourceResolve_Registered(t *testing.T) {
	found := false
	for _, tool := range selfToolDefinitions() {
		if tool.Name == agentSourceResolveToolName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("%q not present in selfToolDefinitions()", agentSourceResolveToolName)
	}
}
