package dispatch

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/classify"
)

// TestIsChatSurfaceTool_RejectsDangerousTools is the negative half of
// the surface-discipline assertion — work-execution tools and the raw
// spawn primitive must be denied even though some of them carry the
// nanite_ prefix.
func TestIsChatSurfaceTool_RejectsDangerousTools(t *testing.T) {
	denied := []string{
		"dev_read", "dev_write", "dev_edit", "dev_glob", "dev_grep",
		"shell_exec", "bash",
		"web_fetch", "web_search",
		// Uniform MCP-origin names (ADR-002): even with no `mcp__server__`
		// prefix to reject by, these are kept off the Chat surface
		// because they don't match any of ChatToolSurface's allow-list
		// prefixes (`nanite_*`, meta-tools).
		"task_create",        // formerly mcp__engine__task_create
		"memory_write",       // formerly mcp__conduit__memory_write
		"clockwork_task_get", // mux MCP — the most common agent surface tool
		"hadron_run_get",     // hadron MCP
		// raw spawn — Chat dispatches via the high-level primitive, not
		// this one.
		"nanite_spawn_subagent",
		"nanite_subagent_status",
		"nanite_subagent_cancel",
	}
	for _, name := range denied {
		if IsChatSurfaceTool(name) {
			t.Errorf("IsChatSurfaceTool(%q) = true, want false (work-execution / spawn-management tools must NOT be on the Chat surface)", name)
		}
	}
}

// TestIsChatSurfaceTool_PlanStepAddAllowed asserts the SP1 append-steps
// tool is reachable from the Chat surface — covered by the existing
// `nanite_plan_` prefix and not requiring a widening of ChatToolSurface.
// CW-20260430-0001 (SP1).
func TestIsChatSurfaceTool_PlanStepAddAllowed(t *testing.T) {
	if !IsChatSurfaceTool("nanite_plan_step_add") {
		t.Fatal("IsChatSurfaceTool(\"nanite_plan_step_add\") = false; expected true via the nanite_plan_ prefix")
	}
}

// TestAssignRole_TableDriven covers the (tier, pattern) → role mapping.
func TestAssignRole_TableDriven(t *testing.T) {
	cases := []struct {
		name     string
		tier     classify.ScopeTier
		pattern  classify.ExecutionPattern
		wantRole Role
		wantSlug string
		wantMode string
	}{
		{
			name:     "trivial inline → worker (sync)",
			tier:     classify.TierTrivial,
			pattern:  classify.PatternInline,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "small inline → worker (sync)",
			tier:     classify.TierSmall,
			pattern:  classify.PatternInline,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "medium subagent → worker (sync)",
			tier:     classify.TierMedium,
			pattern:  classify.PatternSubagent,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "large subagent → worker (sync)",
			tier:     classify.TierLarge,
			pattern:  classify.PatternSubagent,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "open subagent → planner (sync)",
			tier:     classify.TierOpen,
			pattern:  classify.PatternSubagent,
			wantRole: RolePlanner,
			wantSlug: PlannerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "any background → worker (async)",
			tier:     classify.TierMedium,
			pattern:  classify.PatternBackground,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "async",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssignRole(tc.tier, tc.pattern)
			if got.Role != tc.wantRole {
				t.Errorf("Role = %v, want %v", got.Role, tc.wantRole)
			}
			if got.AgentSlug != tc.wantSlug {
				t.Errorf("AgentSlug = %q, want %q", got.AgentSlug, tc.wantSlug)
			}
			if got.Mode != tc.wantMode {
				t.Errorf("Mode = %q, want %q", got.Mode, tc.wantMode)
			}
		})
	}
}

// fakeLister implements PromptTemplateLister for IsChatRoleAgent tests.
type fakeLister struct {
	templates map[string][]PromptTemplateRef
	err       error
}

func (f *fakeLister) ListPromptTemplatesForAgent(agentID string) ([]PromptTemplateRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.templates[agentID], nil
}

func TestIsChatRoleAgent(t *testing.T) {
	chatTpl := PromptTemplateRef{ID: ChatHarnessTemplateID, Slug: ChatHarnessTemplateSlug}
	otherTpl := PromptTemplateRef{ID: "blt-other-001", Slug: "other-template"}

	cases := []struct {
		name    string
		lister  PromptTemplateLister
		agentID string
		want    bool
		wantErr bool
	}{
		{
			name:    "nil lister → false (no error)",
			lister:  nil,
			agentID: "file-default",
			want:    false,
		},
		{
			name:    "empty agentID → false",
			lister:  &fakeLister{templates: map[string][]PromptTemplateRef{"file-default": {chatTpl}}},
			agentID: "",
			want:    false,
		},
		{
			name: "agent has chat-role-harness slug → true",
			lister: &fakeLister{templates: map[string][]PromptTemplateRef{
				"file-default": {chatTpl},
			}},
			agentID: "file-default",
			want:    true,
		},
		{
			name: "agent has chat harness ID → true (slug fallback)",
			lister: &fakeLister{templates: map[string][]PromptTemplateRef{
				"file-default": {{ID: ChatHarnessTemplateID, Slug: "renamed"}},
			}},
			agentID: "file-default",
			want:    true,
		},
		{
			name: "agent has only unrelated templates → false",
			lister: &fakeLister{templates: map[string][]PromptTemplateRef{
				"agent-2": {otherTpl},
			}},
			agentID: "agent-2",
			want:    false,
		},
		{
			name:    "lister error → false + propagated error",
			lister:  &fakeLister{err: errors.New("db down")},
			agentID: "agent-X",
			want:    false,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsChatRoleAgent(tc.lister, tc.agentID)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("IsChatRoleAgent = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsChatSurfaceTool_AcceptsToolListPrimitive is the positive
// surface check for SP6 (CW-20260430-0006). The cheap discovery
// primitive nanite_tool_list must be on the Chat surface so the
// agent can browse the inventory without burning turns guessing
// tool names.
func TestIsChatSurfaceTool_AcceptsToolListPrimitive(t *testing.T) {
	if !IsChatSurfaceTool("nanite_tool_list") {
		t.Error("IsChatSurfaceTool(\"nanite_tool_list\") = false, want true (SP6 cheap-discovery primitive must be on the Chat surface)")
	}
	// Sibling sanity check: nanite_tool_describe is the heavier
	// counterpart and must also be on the surface.
	if !IsChatSurfaceTool("nanite_tool_describe") {
		t.Error("IsChatSurfaceTool(\"nanite_tool_describe\") = false, want true")
	}
}

// TestIsChatSurfaceTool_AcceptsRemindersAndPins is the positive surface
// check for SP2 (CW-20260430-0002). Reminders and pins were always meant
// to be Chat-loop primitives — c120 surfaced that they could be
// described but not called because EnforceChatSurface stripped them.
// Each tool name is exact, sibling style to nanite_remember /
// nanite_validate / nanite_panel_open.
func TestIsChatSurfaceTool_AcceptsRemindersAndPins(t *testing.T) {
	for _, name := range []string{
		"nanite_set_reminder",
		"nanite_pin",
		"nanite_unpin",
	} {
		if !IsChatSurfaceTool(name) {
			t.Errorf("IsChatSurfaceTool(%q) = false, want true (SP2 — reminder/pin Chat-loop primitive must be on the Chat surface)", name)
		}
	}
}

// TestIsChatSurfaceTool_AcceptsMemoryRecall is the positive surface
// check for SP3 (CW-20260430-0003). nanite_memory_recall is the Layer 4
// read-side complement to nanite_remember; without it on the Chat
// surface, lessons captured in past sessions are dead weight.
func TestIsChatSurfaceTool_AcceptsMemoryRecall(t *testing.T) {
	if !IsChatSurfaceTool("nanite_memory_recall") {
		t.Error("IsChatSurfaceTool(\"nanite_memory_recall\") = false, want true (SP3 — Layer 4 read-side complement to nanite_remember must be on the Chat surface)")
	}
	// Sibling sanity: the write side (nanite_remember) must already be on
	// the surface — no Layer 4 closure if either half is missing.
	if !IsChatSurfaceTool("nanite_remember") {
		t.Error("IsChatSurfaceTool(\"nanite_remember\") = false, want true")
	}
}

// TestRole_StringAndIsValid covers the enum helpers.
func TestRole_StringAndIsValid(t *testing.T) {
	cases := []struct {
		r       Role
		str     string
		isValid bool
	}{
		{RoleInvalid, "invalid", false},
		{RoleChat, "chat", true},
		{RoleWorker, "worker", true},
		{RolePlanner, "planner", true},
		{Role(99), "invalid", false},
	}
	for _, c := range cases {
		if got := c.r.String(); got != c.str {
			t.Errorf("Role(%d).String() = %q, want %q", c.r, got, c.str)
		}
		if got := c.r.IsValid(); got != c.isValid {
			t.Errorf("Role(%d).IsValid() = %v, want %v", c.r, got, c.isValid)
		}
	}
}
