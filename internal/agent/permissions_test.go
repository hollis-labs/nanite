package agent

import (
	"encoding/json"
	"testing"
)

// CW-20260417-0487: tool_permissions frontmatter on a file-based agent must
// surface through ToProfile so file agents can express deny rules and call
// budgets without being seeded into agent_profiles.
func TestParseMD_ToolPermissions(t *testing.T) {
	data := []byte(`---
name: Restricted Agent
slug: restricted
toolPermissions:
  allow_list:
    - mcp__dev__*
  deny_list:
    - mcp__dev__dev_bash
  max_calls_per_turn: 8
---
A restricted agent.
`)

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("ParseMD: %v", err)
	}
	if def.ToolPermissions == nil {
		t.Fatal("ToolPermissions should be non-nil when frontmatter sets it")
	}
	if len(def.ToolPermissions.AllowList) != 1 || def.ToolPermissions.AllowList[0] != "mcp__dev__*" {
		t.Errorf("AllowList = %v", def.ToolPermissions.AllowList)
	}
	if len(def.ToolPermissions.DenyList) != 1 || def.ToolPermissions.DenyList[0] != "mcp__dev__dev_bash" {
		t.Errorf("DenyList = %v", def.ToolPermissions.DenyList)
	}
	if def.ToolPermissions.MaxCallsPerTurn != 8 {
		t.Errorf("MaxCallsPerTurn = %d, want 8", def.ToolPermissions.MaxCallsPerTurn)
	}
}

func TestDefinition_ToProfile_PrefersToolPermissionsFrontmatter(t *testing.T) {
	def := &Definition{
		Slug:  "restricted",
		Tools: []string{"read", "write"}, // would normally produce allow_list=[read,write]
		ToolPermissions: &AgentToolPermissions{
			AllowList:       []string{"mcp__dev__*"},
			DenyList:        []string{"mcp__dev__dev_bash"},
			MaxCallsPerTurn: 8,
		},
	}
	p := def.ToProfile()

	var got map[string]any
	if err := json.Unmarshal([]byte(p.ToolPermissions), &got); err != nil {
		t.Fatalf("ToolPermissions JSON: %v", err)
	}
	allow, _ := got["allow_list"].([]any)
	if len(allow) != 1 || allow[0] != "mcp__dev__*" {
		t.Errorf("allow_list = %v, want [mcp__dev__*] (frontmatter overrides Tools)", allow)
	}
	deny, _ := got["deny_list"].([]any)
	if len(deny) != 1 || deny[0] != "mcp__dev__dev_bash" {
		t.Errorf("deny_list = %v", deny)
	}
	if got["max_calls_per_turn"] != float64(8) {
		t.Errorf("max_calls_per_turn = %v", got["max_calls_per_turn"])
	}
}

func TestDefinition_ToProfile_FallsBackToToolsAllowList(t *testing.T) {
	// No ToolPermissions frontmatter — derive allow_list from Tools (existing behavior).
	def := &Definition{
		Slug:  "scoped",
		Tools: []string{"read", "write"},
	}
	p := def.ToProfile()

	var got map[string]any
	if err := json.Unmarshal([]byte(p.ToolPermissions), &got); err != nil {
		t.Fatalf("ToolPermissions JSON: %v", err)
	}
	allow, _ := got["allow_list"].([]any)
	if len(allow) != 2 || allow[0] != "read" || allow[1] != "write" {
		t.Errorf("allow_list = %v, want [read write]", allow)
	}
}
