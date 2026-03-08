package api

import (
	"net/http"
)

// handleListTools returns all registered tools.
// GET /api/tools
func (a *API) handleListTools(w http.ResponseWriter, r *http.Request) {
	if a.ToolBroker == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	tools := a.ToolBroker.ListTools()

	type toolItem struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	items := make([]toolItem, 0, len(tools))
	for _, t := range tools {
		items = append(items, toolItem{
			Name:        t.Name,
			Description: t.Description,
		})
	}

	a.jsonResp(w, http.StatusOK, items)
}

// handleListToolServers returns MCP server info.
// GET /api/tools/servers
func (a *API) handleListToolServers(w http.ResponseWriter, r *http.Request) {
	if a.ToolBroker == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	servers := a.ToolBroker.ListServers()
	a.jsonResp(w, http.StatusOK, servers)
}

// handleSelectTools previews tool selection for an intent.
// POST /api/tools/select
func (a *API) handleSelectTools(w http.ResponseWriter, r *http.Request) {
	if a.ToolBroker == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "tool broker not configured")
		return
	}

	var req struct {
		Intent string   `json:"intent"`
		Hints  []string `json:"hints"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Intent == "" {
		req.Intent = "*"
	}

	tools, err := a.ToolBroker.SelectTools(r.Context(), req.Intent, req.Hints, "", "")
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, tools)
}

// handleRefreshTools triggers MCP tool discovery and syncs with skills table.
// POST /api/tools/refresh
func (a *API) handleRefreshTools(w http.ResponseWriter, r *http.Request) {
	if a.MCPManager == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	diff, err := a.MCPManager.AutoDiscover(r.Context(), a.Store)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, diff)
}

// handleListAgentTools returns tools available to a specific agent (filtered by permissions).
// GET /api/agents/{id}/tools
func (a *API) handleListAgentTools(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	if a.ToolBroker == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	// Get all tools and filter by agent permissions.
	allTools := a.ToolBroker.ListTools()
	perms := a.ToolBroker.GetPermissions(agentID)

	type toolItem struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Allowed     bool   `json:"allowed"`
	}

	items := make([]toolItem, 0, len(allTools))
	for _, t := range allTools {
		allowed := perms.CheckPermission(t.Name)
		items = append(items, toolItem{
			Name:        t.Name,
			Description: t.Description,
			Allowed:     allowed,
		})
	}

	a.jsonResp(w, http.StatusOK, items)
}
