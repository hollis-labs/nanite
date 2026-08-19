package service

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/override"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestResolveAgentCascade_AllThreeTiers is the "Done means" test required
// by TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md: verifies
// closest-wins resolution for system_prompt, class, model/provider, and
// tool/skill defaults across all three cascade tiers (role, broadest ->
// agent/composition -> task/invocation, narrowest).
func TestResolveAgentCascade_AllThreeTiers(t *testing.T) {
	role := &store.Role{
		ID:              "role-1",
		Slug:            "sme",
		Name:            "SME",
		SystemPrompt:    "role-level persona",
		DefaultClass:    "advisor",
		DefaultModel:    "role-model",
		DefaultProvider: "role-provider",
		DefaultTools:    `["role_tool_a","role_tool_b"]`,
		DefaultSkills:   `["role-skill"]`,
	}

	// The agent composition leaves SystemPrompt/Class/Model unset (empty)
	// to prove role defaults flow through when the middle tier doesn't
	// override a field, and sets Provider to prove the middle tier wins
	// over the role's default when it does supply a scalar value. Tools
	// uses the override package's own +/- union semantics (lists are not
	// last-writer-wins like scalars -- see internal/agent/override/merge.go):
	// the agent layer removes one role default ("-role_tool_b") and adds
	// its own tool, demonstrating the nearer tier can both add to and
	// subtract from a broader tier's list default, closest wins per entry.
	profile := &store.AgentProfile{
		ID:              "agent-1",
		SystemPrompt:    "", // inherits role's system prompt
		Class:           "", // inherits role's class
		DefaultModel:    "", // inherits role's model
		DefaultProvider: "agent-provider",
		Tools:           `["-role_tool_b","agent_tool"]`,
		RoleSkills:      `[]`, // inherits role's skills (empty override = no skills override)
	}

	taskOverride := &override.OverrideConfig{
		Class: "process", // task overrides the role-inherited class
		Tools: []string{"+task_tool"},
	}

	result := ResolveAgentCascade(role, profile, taskOverride)

	if result.SystemPrompt != "role-level persona" {
		t.Errorf("SystemPrompt: got %q, want role default %q (agent left it unset)", result.SystemPrompt, "role-level persona")
	}
	if result.Class != "process" {
		t.Errorf("Class: got %q, want task override %q to win over role default %q", result.Class, "process", role.DefaultClass)
	}
	if result.Model != "role-model" {
		t.Errorf("Model: got %q, want role default %q (agent left it unset)", result.Model, "role-model")
	}
	if result.Provider != "agent-provider" {
		t.Errorf("Provider: got %q, want agent-level override %q to win over role default %q", result.Provider, "agent-provider", role.DefaultProvider)
	}

	gotTools := append([]string{}, result.Tools...)
	sort.Strings(gotTools)
	wantTools := []string{"agent_tool", "role_tool_a", "task_tool"}
	if !reflect.DeepEqual(gotTools, wantTools) {
		t.Errorf("Tools: got %v, want %v (role_tool_b removed by the agent layer, role_tool_a survives, agent_tool/task_tool added)", gotTools, wantTools)
	}

	if !reflect.DeepEqual(result.Skills, []string{"role-skill"}) {
		t.Errorf("Skills: got %v, want [role-skill] (agent's empty RoleSkills is not an override, so the role default flows through)", result.Skills)
	}
}

// TestResolveAgentCascade_NilRoleAndTask verifies the resolver degrades to
// a pure passthrough of the agent's own values when neither a role nor a
// task-level override is present -- the exact shape resolveForSession
// exercises today (role is always nil since agent_profiles.role_id
// doesn't exist until 02-add-agents-composition-columns.md; no caller
// supplies a task-level override yet).
func TestResolveAgentCascade_NilRoleAndTask(t *testing.T) {
	profile := &store.AgentProfile{
		SystemPrompt:    "agent persona",
		Class:           "process",
		DefaultModel:    "agent-model",
		DefaultProvider: "agent-provider",
		Tools:           `["a","b"]`,
	}

	result := ResolveAgentCascade(nil, profile, nil)

	if result.SystemPrompt != profile.SystemPrompt {
		t.Errorf("SystemPrompt: got %q, want %q", result.SystemPrompt, profile.SystemPrompt)
	}
	if result.Class != profile.Class {
		t.Errorf("Class: got %q, want %q", result.Class, profile.Class)
	}
	if result.Model != profile.DefaultModel {
		t.Errorf("Model: got %q, want %q", result.Model, profile.DefaultModel)
	}
	if result.Provider != profile.DefaultProvider {
		t.Errorf("Provider: got %q, want %q", result.Provider, profile.DefaultProvider)
	}
	if !reflect.DeepEqual(result.Tools, []string{"a", "b"}) {
		t.Errorf("Tools: got %v, want [a b]", result.Tools)
	}
}

// TestApplyScalarCascade_NoOpWhenRoleAndTaskAbsent is the regression guard
// for resolveForSession's live wiring: with role_id not yet backing any
// row and no task-level override caller, applyScalarCascade must return a
// profile whose four cascade-relevant scalars are byte-identical to the
// input -- the resolveForSession call site's behavior must not change
// until 02-add-agents-composition-columns.md makes role real.
func TestApplyScalarCascade_NoOpWhenRoleAndTaskAbsent(t *testing.T) {
	profile := &store.AgentProfile{
		ID:              "agent-1",
		Name:            "Test",
		SystemPrompt:    "existing persona",
		Class:           "advisor",
		DefaultModel:    "existing-model",
		DefaultProvider: "existing-provider",
	}

	got := applyScalarCascade(profile, nil, nil)

	if got.SystemPrompt != profile.SystemPrompt {
		t.Errorf("SystemPrompt changed: got %q, want %q", got.SystemPrompt, profile.SystemPrompt)
	}
	if got.Class != profile.Class {
		t.Errorf("Class changed: got %q, want %q", got.Class, profile.Class)
	}
	if got.DefaultModel != profile.DefaultModel {
		t.Errorf("DefaultModel changed: got %q, want %q", got.DefaultModel, profile.DefaultModel)
	}
	if got.DefaultProvider != profile.DefaultProvider {
		t.Errorf("DefaultProvider changed: got %q, want %q", got.DefaultProvider, profile.DefaultProvider)
	}
}

// TestAgentService_ResolveForSession_CascadeIsNoOpToday is an end-to-end
// regression guard through the real service seam (resolveForSession),
// confirming the Phase 1 item 01 cascade wiring doesn't alter today's
// resolved agent fields.
func TestAgentService_ResolveForSession_CascadeIsNoOpToday(t *testing.T) {
	reader := newStubReader()
	profile := &store.AgentProfile{
		ID:              "agent-cascade",
		Name:            "Cascade",
		Slug:            "cascade",
		Status:          "active",
		SystemPrompt:    "unchanged persona",
		Class:           "template",
		DefaultModel:    "unchanged-model",
		DefaultProvider: "unchanged-provider",
	}
	reader.addAgent(profile)
	reader.sessionBind["sess-cascade"] = &store.SessionAgent{
		SessionID: "sess-cascade", AgentID: "agent-cascade", Mode: "default", IsPrimary: true,
	}

	svc := NewAgentService(AgentServiceConfig{
		Agents:  reader,
		Writers: &stubAgentWriter{},
	})

	got, err := svc.ResolveForSession(context.Background(), "sess-cascade")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if got.SystemPrompt != profile.SystemPrompt {
		t.Errorf("SystemPrompt: got %q, want %q", got.SystemPrompt, profile.SystemPrompt)
	}
	if got.Class != profile.Class {
		t.Errorf("Class: got %q, want %q", got.Class, profile.Class)
	}
	if got.DefaultModel != profile.DefaultModel {
		t.Errorf("DefaultModel: got %q, want %q", got.DefaultModel, profile.DefaultModel)
	}
	if got.DefaultProvider != profile.DefaultProvider {
		t.Errorf("DefaultProvider: got %q, want %q", got.DefaultProvider, profile.DefaultProvider)
	}
}

// TestRoleOverrideConfig_NilRole verifies the zero-value contract used by
// resolveForSession's current nil-role call.
func TestRoleOverrideConfig_NilRole(t *testing.T) {
	got := RoleOverrideConfig(nil)
	want := override.OverrideConfig{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RoleOverrideConfig(nil) = %+v, want zero value", got)
	}
}

// TestAgentOverrideConfig_DecodesJSONDefaults verifies malformed/absent
// JSON columns degrade to nil slices/maps rather than erroring, matching
// decodeJSONStringArray/decodeJSONObject's fail-open contract.
func TestAgentOverrideConfig_DecodesJSONDefaults(t *testing.T) {
	profile := &store.AgentProfile{
		Tools:           "not-json",
		RoleSkills:      "",
		ToolPermissions: "{}",
	}
	got := AgentOverrideConfig(profile)
	if got.Tools != nil {
		t.Errorf("Tools: got %v, want nil for malformed JSON", got.Tools)
	}
	if got.Skills != nil {
		t.Errorf("Skills: got %v, want nil for empty string", got.Skills)
	}
	if len(got.Permissions) != 0 {
		t.Errorf("Permissions: got %v, want empty for '{}'", got.Permissions)
	}
}
