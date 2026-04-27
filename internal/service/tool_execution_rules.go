package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/toolclient"
)

// enforceExecutionRules re-checks a subset of selection-broker rules at
// execute time. This catches cases where the agent config changed between
// tool selection and tool execution within the same turn.
//
// Checks:
//   - Agent tools allowlist (AgentProfile.Tools JSON array).
//   - Agent permission deny/allow list (ToolPermissions).
//
// Returns (allowed bool, reason string). If allowed is false, reason describes
// the denial for the LLM.
func (s *chatServiceImpl) enforceExecutionRules(ctx context.Context, agentID, toolName string) (bool, string) {
	if s.tools == nil {
		return true, ""
	}

	// Look up agent config via the agents service.
	if s.agents == nil {
		return true, ""
	}

	agent, err := s.agents.Get(ctx, agentID)
	if err != nil {
		slog.Warn("tool-execution-rules: agent lookup failed — allowing",
			"agent", agentID, "err", err)
		return true, ""
	}

	// Check agent tools allowlist (same logic as selection-time filterToolsByAllowlist).
	if agent.Tools != "" && agent.Tools != "[]" {
		var allowlist []string
		if err := json.Unmarshal([]byte(agent.Tools), &allowlist); err == nil && len(allowlist) > 0 {
			allowed := false
			for _, pattern := range allowlist {
				if toolclient.MatchPattern(pattern, toolName) {
					allowed = true
					break
				}
			}
			if !allowed {
				return false, "tool not in agent's allowed tool set"
			}
		}
	}

	// Check permission deny/allow via ToolClient.
	if s.permissions != nil {
		perms := toolclient.ParsePermissions(agent.ToolPermissions)
		if !perms.CheckPermission(toolName) {
			return false, "tool denied by agent permission policy"
		}
	}

	return true, ""
}

// enforceExecutionRulesForTool applies enforceExecutionRules to a tool name.
// With MCP internalization (ADR-002) the tool name is uniform — there is
// no `mcp__server__` prefix to strip, so the historical bare-name fallback
// check is no longer needed.
func (s *chatServiceImpl) enforceExecutionRulesForTool(ctx context.Context, agentID, toolName string) (bool, string) {
	return s.enforceExecutionRules(ctx, agentID, toolName)
}
