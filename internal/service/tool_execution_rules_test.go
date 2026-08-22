package service

import (
	"context"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// TestAgentToolsGrant_SelectionAndExecutionBothAccept is the Done-means
// integration test TASKS/phase-5/01-build-assignment-api.md's bullet 4
// calls for: a tool granted via agent_tools (this task's REST grant
// endpoint's store-layer counterpart) that is NOT in the agent's legacy
// agent_profiles.tools column must be both (a) visible in SelectForAgent's
// output and (b) accepted by enforceExecutionRules' execution-time
// re-check -- closing the gap TASKS/phase-4/05's Work Log item 6 flagged
// (enforceExecutionRules used to only ever consult the legacy `tools`
// column, completely independent of SelectForAgent's post-05 agent_tools
// read path).
func TestAgentToolsGrant_SelectionAndExecutionBothAccept(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	t.Cleanup(func() { st.Close(context.Background()) })

	agent := &store.AgentProfile{
		Name:         "Grant Turn Agent",
		Slug:         "grant-turn-agent",
		SystemPrompt: "You are a test agent.",
		// The legacy schema-v2 allowlist column deliberately does NOT
		// contain the tool this test grants via agent_tools -- proving the
		// grant (not the legacy column) is what makes both selection and
		// execution accept it.
		Tools: `["some_other_legacy_tool"]`,
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	const grantedTool = "assignment_api_granted_tool"
	toolID, err := st.UpsertKnownTool(ctx, grantedTool, "builtin", "available", "a test tool")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}
	if err := st.GrantAgentTool(ctx, agent.ID, toolID, "explicit"); err != nil {
		t.Fatalf("GrantAgentTool: %v", err)
	}

	// --- Selection side: SelectForAgent must offer the granted tool. ---
	tc := toolclient.New(nil, st, nil)
	tc.Builtins.RegisterBuiltins("general", []llmtypes.ToolDefinition{
		{Name: grantedTool, Description: "a test tool"},
	})
	toolSvc := NewToolService(tc, nil, st)

	selection, err := toolSvc.SelectForAgent(ctx, "sess-grant-turn", agent.ID, "do something", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}
	found := false
	for _, tool := range selection.Tools {
		if tool.Name == grantedTool {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SelectForAgent did not offer %q; got tools: %+v", grantedTool, selection.Tools)
	}

	// --- Execution side: enforceExecutionRules must accept the same tool. ---
	agentSvc := NewAgentService(AgentServiceConfig{Agents: st, Writers: st})
	chatSvc := &chatServiceImpl{
		agents: agentSvc,
		tools:  toolSvc,
		store:  st,
	}
	allowed, reason := chatSvc.enforceExecutionRules(ctx, agent.ID, grantedTool)
	if !allowed {
		t.Fatalf("enforceExecutionRules rejected a tool granted via agent_tools: reason=%q", reason)
	}
}

// TestEnforceExecutionRules_AgentToolsRejectsUngrantedTool is the negative
// counterpart: a real agent_profiles-backed agent's execution-time gate
// must still reject a tool that was never granted via agent_tools, even
// though enforceExecutionRulesViaAgentTools no longer consults the legacy
// `tools` column at all for this population.
func TestEnforceExecutionRules_AgentToolsRejectsUngrantedTool(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	t.Cleanup(func() { st.Close(context.Background()) })

	agent := &store.AgentProfile{
		Name:         "Ungranted Agent",
		Slug:         "ungranted-agent",
		SystemPrompt: "You are a test agent.",
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	agentSvc := NewAgentService(AgentServiceConfig{Agents: st, Writers: st})
	toolSvc := NewToolService(toolclient.New(nil, st, nil), nil, st)
	chatSvc := &chatServiceImpl{agents: agentSvc, tools: toolSvc, store: st}

	allowed, reason := chatSvc.enforceExecutionRules(ctx, agent.ID, "never_granted_tool")
	if allowed {
		t.Fatalf("enforceExecutionRules allowed an ungranted tool for a real agent_profiles-backed agent; reason=%q", reason)
	}
}

// TestEnforceExecutionRules_AgentToolsAllowsAlwaysIncluded confirms the
// known_tools.always_included escape hatch survives the execution-time
// re-check the same way it survives selection (resolveAlwaysIncludedTools)
// -- otherwise granting via agent_tools would regress every DB-backed
// agent's ability to actually call request_tools/tool_list/tool_describe.
func TestEnforceExecutionRules_AgentToolsAllowsAlwaysIncluded(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	t.Cleanup(func() { st.Close(context.Background()) })

	agent := &store.AgentProfile{
		Name:         "Always Included Agent",
		Slug:         "always-included-agent",
		SystemPrompt: "You are a test agent.",
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := st.UpsertKnownTool(ctx, "request_tools", "builtin", "available", "escape hatch"); err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE known_tools SET always_included = TRUE WHERE name = 'request_tools'`); err != nil {
		t.Fatalf("mark always_included: %v", err)
	}

	agentSvc := NewAgentService(AgentServiceConfig{Agents: st, Writers: st})
	toolSvc := NewToolService(toolclient.New(nil, st, nil), nil, st)
	chatSvc := &chatServiceImpl{agents: agentSvc, tools: toolSvc, store: st}

	allowed, reason := chatSvc.enforceExecutionRules(ctx, agent.ID, "request_tools")
	if !allowed {
		t.Fatalf("enforceExecutionRules rejected the always_included escape hatch tool: reason=%q", reason)
	}
}
