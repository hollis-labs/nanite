package api

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
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

	// TASKS/phase-4/05's Work Log item 3 (the stale-deny-list footgun) used
	// to reject a grant here outright when the agent's legacy
	// agent_profiles.tool_permissions.deny_list still matched the granted
	// tool name, because tool_permissions.CheckPermission stayed live as a
	// deeper ToolClient.SelectToolsAsProvider/CallTool backstop that could
	// silently veto a fresh grant. TASKS/adhoc/02-remove-tool-permissions-
	// collapse-to-agent-tools.md removed that backstop entirely -- agent_tools
	// (+ the known_tools.always_included escape hatch) is now the sole gate
	// at every layer, including CallTool -- so there is no deeper mechanism
	// left for a stale deny_list to veto, and this pre-flight check is
	// removed along with it.
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
