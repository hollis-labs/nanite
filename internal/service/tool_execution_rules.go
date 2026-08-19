package service

import (
	"context"
	"log/slog"
)

// enforceExecutionRules re-checks a subset of selection-broker rules at
// execute time. This catches cases where the agent config changed between
// tool selection and tool execution within the same turn.
//
// agent_tools grant membership (+ the known_tools.always_included escape
// hatch), via enforceExecutionRulesViaAgentTools, is the sole gate for
// every agentID, unconditionally --
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md
// removed the legacy Agent-tools-allowlist/ToolPermissions re-check that
// used to run here for agentIDs with no real agent_profiles row (only ever
// file-based agents, eliminated by
// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md).
//
// TASKS/phase-5/01-build-assignment-api.md closed a real gap here: before
// that change, this function unconditionally re-checked the legacy
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

	// Look up agent config via the agents service. Every agent (including
	// the compiled-in builtin profiles) is a real agent_profiles row as of
	// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md -- a lookup
	// failure here means agentID itself doesn't resolve to a known agent,
	// so fail open (allow) rather than reject a legitimate call over a
	// stale/unresolvable ID, matching this function's pre-existing
	// missing-wiring precedent below.
	if s.agents == nil {
		return true, ""
	}
	if _, err := s.agents.Get(ctx, agentID); err != nil {
		slog.Warn("tool-execution-rules: agent lookup failed — allowing",
			"agent", agentID, "err", err)
		return true, ""
	}
	if s.store == nil {
		return true, ""
	}

	return s.enforceExecutionRulesViaAgentTools(ctx, agentID, toolName)
}

// enforceExecutionRulesViaAgentTools is the agent_tools-authoritative
// execution-time re-check, unconditionally, for every agentID -- mirrors
// filterToolsByAgentTools' exact scoping (TASKS/phase-4/05's Work Log items
// 1/2/5). agent_tools membership (+ the known_tools.always_included escape
// hatch) is the sole gate; tool_permissions no longer exists anywhere in
// the system as of TASKS/adhoc/02-remove-tool-permissions-collapse-to-
// agent-tools.md (including the deeper ToolClient.SelectToolsAsProvider/
// CallTool layer, which that task also collapsed onto agent_tools).
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
