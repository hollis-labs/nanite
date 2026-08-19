package mcp

import (
	"context"
	"encoding/json"

	"github.com/hollis-labs/nanite/internal/a2a"
)

// whoamiToolDefinition returns the whoami self-tool — agent address
// self-discovery (closes CW-20260519-0069).
//
// Returns the calling agent's canonical msg:// address. Enables agents to
// reference themselves in A2A protocol interactions without hardcoding or
// guessing their own address. Dispatched via executeWhoami below (see the
// "whoami" case in CallTool) since Tool itself carries no handler field —
// self-tools are dispatched by name, not by an embedded closure.
func whoamiToolDefinition() Tool {
	return Tool{
		Name: "whoami",
		Description: "Returns the calling agent's canonical msg:// address for A2A protocol self-discovery.\n\n" +
			"**When to use:** When the agent needs to reference its own address in A2A protocol interactions, " +
			"external API calls, or self-referential operations.\n\n" +
			"**Output shape:** `{\"address\": \"msg://agent/nanite/agt_...\"}` — the canonical msg:// URN " +
			"that other agents use to target this agent.\n\n" +
			"**Note:** This is the durable-agent instance address. For ephemeral/template agents, " +
			"the address is newly generated on each boot; for persistent agents, it survives across wakes.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// executeWhoami implements the whoami self-tool. The calling agent's
// identity comes from the (workspace_id, agent_profile_id) pair stamped
// onto ctx by chatServiceImpl.executeToolBatch (mcp.WithCallerProfile) —
// the same H1 trust-gate mechanism the dispatch subsystem already relies
// on, not a separate identity lookup.
func (st *SelfToolsTransport) executeWhoami(ctx context.Context, args map[string]any) (*ToolResult, error) {
	agentID := CallerProfileFromContext(ctx)
	if agentID == "" {
		return errorResult("whoami: no agent profile in context (not called from a durable-agent session)"), nil
	}

	addr := a2a.NewAgentAddress(agentID)
	out, err := json.Marshal(map[string]any{
		"address": addr.URN(),
	})
	if err != nil {
		return errorResult("whoami: " + err.Error()), nil
	}
	return textResult(string(out)), nil
}
