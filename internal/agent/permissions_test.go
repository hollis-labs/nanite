package agent

import (
	"testing"
)

// TestParseMD_ToolPermissions, TestDefinition_ToProfile_
// PrefersToolPermissionsFrontmatter, and TestDefinition_ToProfile_
// FallsBackToToolsAllowList (CW-20260417-0487) were removed by
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md: the
// toolPermissions: frontmatter field and AgentToolPermissions no longer
// exist, and Definition.ToProfile() always writes "{}" into
// store.AgentProfile.ToolPermissions now regardless of Tools -- see
// TestDefinition_ToProfile_Minimal in convert_test.go for that coverage.

// CW-20260512-0107 (SP-20260512-0008 W2A): parentDispatchAllowlist
// frontmatter on a file-based agent must surface through ToProfile so the
// Tool Broker Describer renders the per-caller dispatch enumeration.
func TestParseMD_ParentDispatchAllowlist(t *testing.T) {
	data := []byte(`---
name: Trusted Parent
slug: trusted-parent
parentDispatchAllowlist:
  - researcher
  - planner
  - worker
---
A trusted parent.
`)

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("ParseMD: %v", err)
	}
	want := []string{"researcher", "planner", "worker"}
	if len(def.ParentDispatchAllowlist) != len(want) {
		t.Fatalf("len(ParentDispatchAllowlist) = %d, want %d", len(def.ParentDispatchAllowlist), len(want))
	}
	for i, s := range want {
		if def.ParentDispatchAllowlist[i] != s {
			t.Errorf("ParentDispatchAllowlist[%d] = %q, want %q", i, def.ParentDispatchAllowlist[i], s)
		}
	}
}

func TestDefinition_ToProfile_ParentDispatchAllowlistMarshalsJSON(t *testing.T) {
	def := &Definition{
		Slug:                    "trusted",
		ParentDispatchAllowlist: []string{"researcher", "planner", "worker"},
	}
	p := def.ToProfile()
	if p.ParentDispatchAllowlist != `["researcher","planner","worker"]` {
		t.Errorf("ParentDispatchAllowlist JSON = %q, want canonical 3-role list", p.ParentDispatchAllowlist)
	}
}

// Empty frontmatter list must serialize to "[]" so the schema column
// default invariant holds for file-based agents.
func TestDefinition_ToProfile_ParentDispatchAllowlistEmptyDefaultsToBracketBracket(t *testing.T) {
	def := &Definition{Slug: "legacy"}
	p := def.ToProfile()
	if p.ParentDispatchAllowlist != "[]" {
		t.Errorf("ParentDispatchAllowlist (empty frontmatter) = %q, want \"[]\"", p.ParentDispatchAllowlist)
	}
}
