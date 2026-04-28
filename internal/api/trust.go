package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/dispatch"
)

// handleSetWorkspaceRoleTrust handles
//
//	POST /api/workspaces/{workspace_id}/roles/{agent_profile_id}/trust
//
// Body: {"tier": "trusted"|"normal"|"untrusted", "promoted_by": "..."}
func (a *API) handleSetWorkspaceRoleTrust(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspace_id")
	agentProfileID := r.PathValue("agent_profile_id")
	if workspaceID == "" || agentProfileID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id and agent_profile_id are required")
		return
	}

	var req SetWorkspaceRoleTrustRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Tier == "" {
		a.errorResp(w, http.StatusBadRequest, "tier is required (untrusted|normal|trusted)")
		return
	}

	tier := dispatch.TrustTier(req.Tier)
	if !tier.IsValid() {
		a.errorResp(w, http.StatusBadRequest, "tier must be one of: untrusted, normal, trusted")
		return
	}

	promotedBy := req.PromotedBy
	if promotedBy == "" {
		promotedBy = "api"
	}

	if err := a.Services.Store.PromoteRoleInWorkspace(r.Context(), workspaceID, agentProfileID, tier, promotedBy); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{
		"workspace_id":     workspaceID,
		"agent_profile_id": agentProfileID,
		"trust_tier":       string(tier),
		"promoted_by":      promotedBy,
	})
}

// handleDeleteWorkspaceRoleTrust handles
//
//	DELETE /api/workspaces/{workspace_id}/roles/{agent_profile_id}/trust
//
// Removes the workspace-scoped override; role falls back to its
// agent_profiles.default_trust_tier.
func (a *API) handleDeleteWorkspaceRoleTrust(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspace_id")
	agentProfileID := r.PathValue("agent_profile_id")
	if workspaceID == "" || agentProfileID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id and agent_profile_id are required")
		return
	}

	if err := a.Services.Store.DemoteRoleInWorkspace(r.Context(), workspaceID, agentProfileID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleListWorkspaceRoleTrust handles
//
//	GET /api/workspaces/{workspace_id}/roles
//
// Returns agent profiles with their effective trust tier for the workspace.
func (a *API) handleListWorkspaceRoleTrust(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspace_id")
	if workspaceID == "" {
		a.errorResp(w, http.StatusBadRequest, "workspace_id is required")
		return
	}

	overrides, err := a.Services.Store.ListWorkspaceRoleTrust(r.Context(), workspaceID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "trust_overrides": overrides})
}
