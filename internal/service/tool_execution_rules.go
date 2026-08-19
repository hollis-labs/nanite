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
// Checks, for any agentID that resolves to a real agent_profiles row
// (mirroring SelectForAgent's own dbAgent split -- filterToolsByAgentTools,
// TASKS/phase-4/05's Work Log item 1):
//   - agent_tools grant membership (+ the known_tools.always_included
//     escape hatch), via enforceExecutionRulesViaAgentTools.
//
// ...and for every other agentID (structurally, today, only a file-based
// agent dispatched under its runtime "file-<slug>" alias, which cannot
// participate in agent_tools -- a real FK to agent_profiles):
//   - Agent tools allowlist (AgentProfile.Tools JSON array, legacy schema-
//     v2 shape).
//   - Agent permission deny/allow list (ToolPermissions).
//
// TASKS/phase-5/01-build-assignment-api.md closed a real gap here: before
// this change, this function unconditionally re-checked the legacy
// agent_profiles.tools column for every agent, completely independent of
// SelectForAgent's post-05 agent_tools-based selection -- so a tool granted
// via that task's own agent_tools API could be offered to the model at
// selection time and then rejected here, one turn later, purely because
// this separate re-check didn't know about the new table
// (TASKS/phase-4/05's Work Log item 6 flagged this explicitly as required
// follow-up work for that task).
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

	// Prefer the agent_tools-authoritative path for any agentID that
	// resolves to a real agent_profiles row. A raw store.GetAgent(agentID)
	// lookup (unlike s.agents.Get above, which also resolves a file-based
	// "file-<slug>" alias to an in-memory profile) misses for anything
	// that isn't a real DB row under that exact ID -- the same
	// distinguishing signal SelectForAgent's own dbAgent check uses
	// (toolServiceImpl.agentToolsStore()-backed filterToolsByAgentTools).
	if s.store != nil {
		if _, err := s.store.GetAgent(agentID); err == nil {
			return s.enforceExecutionRulesViaAgentTools(ctx, agentID, toolName)
		}
	}

	// No real agent_profiles row for agentID (a file-based agent) --
	// unchanged legacy allowlist/permission re-check.
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

// enforceExecutionRulesViaAgentTools is the agent_tools-authoritative
// execution-time re-check for any agentID that resolves to a real
// agent_profiles row -- mirrors filterToolsByAgentTools' exact scoping
// (TASKS/phase-4/05's Work Log items 1/2/5). agent_tools membership (+ the
// known_tools.always_included escape hatch) is the sole gate for this
// population; tool_permissions is deliberately NOT re-consulted a second
// time here, matching SelectForAgent's own decision not to double-gate on
// it at that surface for this population (05's Work Log item 3's "two
// systems of record" tradeoff is about the deeper ToolClient.
// SelectToolsAsProvider/CallTool backstop, which still runs independently
// and is unaffected by this function either way).
func (s *chatServiceImpl) enforceExecutionRulesViaAgentTools(ctx context.Context, agentID, toolName string) (bool, string) {
	granted, err := s.store.ListAgentToolNames(ctx, agentID)
	if err != nil {
		slog.Warn("tool-execution-rules: list agent_tools failed — allowing",
			"agent", agentID, "err", err)
		return true, ""
	}
	for _, name := range granted {
		if name == toolName {
			return true, ""
		}
	}

	// known_tools.always_included escape hatch (request_tools/tool_list/
	// tool_describe) -- must survive execution the same way it survives
	// selection (resolveAlwaysIncludedTools), or granting via agent_tools
	// would regress every DB-backed agent's ability to actually call these
	// tools.
	always, err := s.store.ListAlwaysIncludedKnownTools(ctx)
	if err != nil {
		slog.Warn("tool-execution-rules: list always_included known_tools failed — allowing",
			"agent", agentID, "err", err)
		return true, ""
	}
	for _, t := range always {
		if t.Name == toolName && t.Status == "available" {
			return true, ""
		}
	}

	return false, "tool not in agent's allowed tool set"
}

// enforceExecutionRulesForTool applies enforceExecutionRules to a tool name.
// With MCP internalization (ADR-002) the tool name is uniform — there is
// no `mcp__server__` prefix to strip, so the historical bare-name fallback
// check is no longer needed.
func (s *chatServiceImpl) enforceExecutionRulesForTool(ctx context.Context, agentID, toolName string) (bool, string) {
	return s.enforceExecutionRules(ctx, agentID, toolName)
}
