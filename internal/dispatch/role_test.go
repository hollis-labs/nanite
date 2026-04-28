package dispatch

import (
	"errors"
	"sort"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/classify"
)

// TestEnforceChatSurface_FiltersToStaticAllowList asserts the boot-time
// filter clamps to exactly the static surface and nothing else. The
// surface is intentionally narrow per harness spec §1.
func TestEnforceChatSurface_FiltersToStaticAllowList(t *testing.T) {
	in := []provider.ToolDefinition{
		// Allowed (todos).
		{Name: "nanite_todo_create"},
		{Name: "nanite_todo_list"},
		// Allowed (plans).
		{Name: "nanite_plan_create"},
		// Allowed (scratchpad).
		{Name: "nanite_scratchpad_write"},
		// Allowed (peer query / messaging).
		{Name: "nanite_message_send"},
		{Name: "nanite_handoff_request"},
		// Allowed (narration — generic envelope-emit tool replaces per-type
		// nanite_show_* tools as of CW-20260428-0019).
		{Name: "nanite_show_card"},
		// Allowed (executeTask).
		{Name: "nanite_execute_task"},
		// Allowed (chat_search, P8B).
		{Name: "nanite_chat_search"},
		// Allowed (panel control, J8 v1, CW-20260426-0006).
		{Name: "nanite_panel_open"},
		{Name: "nanite_panel_close"},
		{Name: "nanite_signal_mode"},
		// Meta-tools — always allowed.
		{Name: "fetch_tool_result"},
		{Name: "search_tool_result"},
		{Name: "request_tools"},

		// REJECTED — these are work-execution tools the Chat agent must
		// NOT have access to. Chat dispatches; it does not execute.
		{Name: "dev_read"},
		{Name: "dev_write"},
		{Name: "dev_edit"},
		{Name: "dev_glob"},
		{Name: "dev_grep"},
		{Name: "shell_exec"},
		{Name: "web_fetch"},
		// MCP-origin tools post-internalization (ADR-002) — uniform
		// agent-facing names with no `mcp__server__` prefix. The Chat
		// surface filters them out because they don't match any prefix
		// in ChatToolSurface (no `nanite_*`, no meta-tool).
		{Name: "task_create"},        // formerly mcp__engine__task_create
		{Name: "memory_write"},        // formerly mcp__conduit__memory_write
		{Name: "clockwork_task_get"}, // mux MCP
		// Spawn primitive itself is NOT on the surface — Chat dispatches
		// via executeTask (the high-level primitive), not the raw spawn
		// tool.
		{Name: "nanite_spawn_subagent"},
	}

	got := EnforceChatSurface(in)

	expected := map[string]bool{
		"nanite_todo_create":      true,
		"nanite_todo_list":        true,
		"nanite_plan_create":      true,
		"nanite_scratchpad_write": true,
		"nanite_message_send":     true,
		"nanite_handoff_request":  true,
		"nanite_show_card":        true,
		"nanite_execute_task":     true,
		"nanite_chat_search":      true,
		"nanite_panel_open":       true,
		"nanite_panel_close":      true,
		"nanite_signal_mode":      true,
		"fetch_tool_result":       true,
		"search_tool_result":      true,
		"request_tools":           true,
	}

	gotNames := make([]string, len(got))
	for i, t := range got {
		gotNames[i] = t.Name
	}
	sort.Strings(gotNames)

	// Every surviving tool must be on the allow-list.
	for _, name := range gotNames {
		if !expected[name] {
			t.Errorf("EnforceChatSurface leaked non-surface tool %q (rejected: dev_*, shell_*, web_*, MCP-origin tools, nanite_spawn_subagent must NEVER survive)", name)
		}
	}
	// Every expected tool must have survived.
	if len(got) != len(expected) {
		t.Errorf("EnforceChatSurface dropped allowed tools: got %v, want %v", gotNames, expected)
	}
}

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
