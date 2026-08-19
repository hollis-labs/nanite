package service

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/describer"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// TestParseParentDispatchAllowlist covers the JSON shapes the column can carry.
func TestParseParentDispatchAllowlist(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty string degrades to nil", "", nil},
		{"empty array degrades to nil", "[]", nil},
		{"single slug", `["worker"]`, []string{"worker"}},
		{
			"canonical three-role list",
			`["researcher","planner","worker"]`,
			[]string{"researcher", "planner", "worker"},
		},
		{"malformed JSON degrades to nil", `not-json`, nil},
		{"non-array JSON degrades to nil", `{"key":"value"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseParentDispatchAllowlist(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d, want %d (got=%v want=%v)", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestSelectForAgent_PopulatesDispatchAllowlistFromAgentProfile is the
// production acceptance test for CW-20260512-0107 (SP-20260512-0008 W2A).
//
// Two callers, same tool universe, two task_execute description bodies:
//   - "trusted" parent (chat-role default) with allowlist ["researcher",
//     "planner", "worker"] — task_execute description mentions all three
//     role slugs.
//   - "restricted" parent with allowlist ["worker"] only — task_execute
//     description mentions worker, NOT planner / researcher.
//
// The wiring tested: AgentProfile.ParentDispatchAllowlist → SelectForAgent
// → describer.CallerAgent.DispatchAllowlist → describeTaskExecute.
func TestSelectForAgent_PopulatesDispatchAllowlistFromAgentProfile(t *testing.T) {
	tc := buildToolClientWithTaskExecute(t)

	reader := newStubReader()
	// Trusted parent uses slug "trusted-parent" (NOT the chat-role
	// default slug "default") specifically to bypass the chat-surface
	// filter, which would otherwise strip task_execute from the chat
	// agent's visible surface. The behavior under test is the describer
	// wiring — AgentProfile.ParentDispatchAllowlist propagating through
	// SelectForAgent into the rendered task_execute description — so a
	// non-chat slug keeps the assertion focused on that path. The
	// DispatchAllowlist field is what matters here, not the slug.
	reader.addAgent(&store.AgentProfile{
		ID:                      "trusted-parent",
		Slug:                    "trusted-parent",
		Status:                  "active",
		ParentDispatchAllowlist: `["researcher","planner","worker"]`,
	})
	reader.addAgent(&store.AgentProfile{
		ID:                      "restricted-parent",
		Slug:                    "restricted-parent",
		Status:                  "active",
		ParentDispatchAllowlist: `["worker"]`,
	})

	svc := NewToolService(tc, nil, reader).(*toolServiceImpl)

	descTrusted := taskExecuteDescriptionFor(t, svc, "trusted-parent")
	descRestricted := taskExecuteDescriptionFor(t, svc, "restricted-parent")

	if descTrusted == descRestricted {
		t.Fatalf("descTrusted == descRestricted — DispatchAllowlist did not propagate from AgentProfile -> Describer")
	}
	for _, role := range []string{"researcher", "planner", "worker"} {
		if !strings.Contains(descTrusted, role) {
			t.Errorf("trusted parent description missing role %q: %q", role, descTrusted)
		}
	}
	if !strings.Contains(descRestricted, "worker") {
		t.Errorf("restricted parent description missing worker: %q", descRestricted)
	}
	for _, leak := range []string{"planner", "researcher"} {
		if strings.Contains(descRestricted, leak) {
			t.Errorf("restricted parent leaked role %q not in its allowlist: %q", leak, descRestricted)
		}
	}
}

// TestSelectForAgent_EmptyAllowlistFallsBackToBaseline covers the legacy /
// non-parent profile case: empty parent_dispatch_allowlist column ("[]"
// from migration 059 default) means the Describer falls back to the
// baseline static description with no role enumeration.
func TestSelectForAgent_EmptyAllowlistFallsBackToBaseline(t *testing.T) {
	tc := buildToolClientWithTaskExecute(t)

	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{
		ID:                      "legacy-parent",
		Slug:                    "legacy-parent",
		Status:                  "active",
		ParentDispatchAllowlist: "[]",
	})

	svc := NewToolService(tc, nil, reader).(*toolServiceImpl)

	got := taskExecuteDescriptionFor(t, svc, "legacy-parent")

	// Baseline body must be present; no "Dispatchable roles" section.
	if !strings.Contains(got, "Dispatch a task to a Worker or Planner role agent.") {
		t.Errorf("legacy parent description missing baseline body: %q", got)
	}
	if strings.Contains(got, "Dispatchable roles for this caller") {
		t.Errorf("legacy parent description should NOT carry per-caller dispatch section (empty allowlist): %q", got)
	}
}

// --- helpers ---

// buildToolClientWithTaskExecute wires a toolclient with a stand-in
// task_execute tool registered directly on the catalog plus the production
// describer registered through mcp.RegisterSelfToolDescribers. This lets
// the service-layer test exercise the real describeTaskExecute renderer
// without spinning up the full self-tool stack.
func buildToolClientWithTaskExecute(t *testing.T) *toolclient.ToolClient {
	t.Helper()
	cfg := toolclient.DefaultConfig()
	tc := toolclient.New(nil, nil, cfg)
	tc.RegisterTools([]llmtypes.ToolDefinition{
		{Name: "task_execute", Description: "Dispatch a task — placeholder, overwritten by Describer."},
	})
	// Wire the production describer set so describeTaskExecute is what
	// renders on the SelectForAgent hot path.
	mcp.RegisterSelfToolDescribers(tc.Describers)
	return tc
}

// taskExecuteDescriptionFor runs SelectForAgent and returns the
// task_execute tool's rendered description for the given caller. Fails
// the test if task_execute is missing from the selection (the toolclient
// stand-in always registers it).
func taskExecuteDescriptionFor(t *testing.T, svc *toolServiceImpl, agentID string) string {
	t.Helper()
	sel, err := svc.SelectForAgent(context.Background(), "session-1", agentID, "do something", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent(%s): %v", agentID, err)
	}
	for _, tool := range sel.Tools {
		if tool.Name == "task_execute" {
			return tool.Description
		}
	}
	t.Fatalf("task_execute missing from SelectForAgent result for %s (tools: %v)", agentID, testToolNames(sel.Tools))
	return ""
}

func testToolNames(tools []llmtypes.ToolDefinition) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

// Verify the helper compiles against the contract type.
var _ describer.Describer = describer.Func(nil)
