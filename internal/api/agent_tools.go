package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// Agent tools grant/revoke -- Phase 5 item 01 (TASKS/phase-5/01-build-
// assignment-api.md). Phase 1 item 04 (TASKS/phase-1/04-add-known-tools-
// and-agent-tools-fk.md) built the store-layer grant/revoke functions
// (internal/store/agent_tools.go) and a list-only REST endpoint
// (handleListAgentTools, internal/api/tools.go); this is the write side --
// the real FK-based agent_tools join that TASKS/phase-4/05 wired
// SelectForAgent to read as the sole roster-membership gate for any agent
// with a real agent_profiles row (see that task's Work Log). Per that
// task's own explicit instruction, there is no other live mechanism to
// grant a newly-catalog tool to an agent -- this endpoint is it.
//
// Deliberately gated by requireAgent (existence only), NOT
// requireMutableAgent. agent_known_tools/reflexes/procedures (the other
// per-agent capability write endpoints in agent_capabilities.go) bundle
// their writes with the managed-file-editability gate, but agent_tools is
// a DB-only mechanism structurally independent of file-management status --
// and the internal/embedded agents (worker, default, planner, ...) that
// most need a fresh grant once known_tools gains a new entry (per 05's
// Work Log item 4 -- there is no more live "unrestricted" bypass for a
// tool added after an agent's grants were snapshotted) are exactly the
// non-editable ManageClassInternal population that requireMutableAgent
// would lock out of ever receiving one via this API.

// handleGrantAgentTool grants a known_tools row to the agent in the URL
// path.
// POST /api/agents/{id}/tools
func (a *API) handleGrantAgentTool(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}

	var req GrantAgentToolRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.ToolID == "" {
		a.errorResp(w, http.StatusBadRequest, "tool_id is required")
		return
	}

	tool, err := a.Services.Store.GetKnownTool(r.Context(), req.ToolID)
	if err != nil {
		if errors.Is(err, store.ErrKnownToolNotFound) {
			a.errorResp(w, http.StatusNotFound, "known tool not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// TASKS/phase-4/05's Work Log item 3 (the stale-deny-list footgun):
	// agent_profiles.tool_permissions.deny_list is retired at the
	// SelectForAgent surface for any agent with a real agent_profiles row,
	// but stays live at the deeper ToolClient.SelectToolsAsProvider/
	// CallTool layer as a deliberate defense-in-depth backstop (05's own
	// Work Log item 3) -- so a still-populated legacy deny_list can
	// silently veto a fresh agent_tools grant before it ever reaches the
	// LLM's selection, even though this endpoint reports success.
	//
	// Decision (this task's Work Log): reject the grant outright and name
	// the conflicting pattern, rather than (b) warn-only or (c) clear/
	// neutralize tool_permissions on first grant. (c) was rejected because
	// tool_permissions is not solely a legacy shadow of agent_tools -- it
	// also protects a distinct, already-tested gap (tools reaching
	// allTools via the discoverAgentMCPTools direct-MCP fallback, which
	// bypasses SelectToolsAsProvider's own gate entirely -- CW-20260512-
	// 0117 / SP-20260512-0010, per 05's Work Log item 1.2) that this
	// endpoint has no business silently disabling as a side effect of an
	// unrelated grant call.
	perms := toolclient.ParsePermissions(agent.ToolPermissions)
	if pattern, denied := matchingDenyPattern(perms, tool.Name); denied {
		a.errorResp(w, http.StatusConflict, fmt.Sprintf(
			"grant rejected: agent %q has a legacy tool_permissions.deny_list pattern %q that would still veto tool %q at the deeper execution-time/broker permission backstop, even though this agent_tools grant would otherwise succeed -- clear or narrow that deny_list entry (PUT /api/agents/%s) before granting",
			agent.Slug, pattern, tool.Name, agent.ID,
		))
		return
	}

	grantedVia := req.GrantedVia
	if grantedVia == "" {
		grantedVia = "explicit"
	}
	if err := a.Services.Store.GrantAgentTool(r.Context(), agent.ID, tool.ID, grantedVia); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	names, err := a.Services.Store.ListAgentToolNames(r.Context(), agent.ID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, map[string]any{"tool_names": names})
}

// handleRevokeAgentTool revokes a single agent_tools grant.
// DELETE /api/agents/{id}/tools/{toolId}
func (a *API) handleRevokeAgentTool(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	toolID := r.PathValue("toolId")
	if toolID == "" {
		a.errorResp(w, http.StatusBadRequest, "toolId is required")
		return
	}
	if err := a.Services.Store.RevokeAgentTool(r.Context(), agent.ID, toolID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// requireRealAgentToolsTarget (rejecting a grant/revoke request against an
// agent with no real agent_profiles UUID identity) was removed by
// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md: the file-based
// runtime alias (agent.Definition.CanonicalID's "file-<slug>" fallback)
// this guard existed to detect no longer exists. Every agent resolved via
// a.Services.Agents.Get -- internal/embedded, plugin/vendor, or
// managed/API-created -- now carries its real agent_profiles UUID, so
// grant/revoke can rely on agent.ID directly the same way any other real
// FK-bearing write does.

// matchingDenyPattern reports whether toolName matches any glob in perms'
// deny_list, returning the first matching pattern (deny_list order,
// mirroring ToolPermissions.CheckPermission's own "first match wins"
// iteration) or ("", false) if none match.
func matchingDenyPattern(perms toolclient.ToolPermissions, toolName string) (string, bool) {
	for _, pattern := range perms.DenyList {
		if toolclient.MatchPattern(pattern, toolName) {
			return pattern, true
		}
	}
	return "", false
}
