package api

import (
	"net/http"
	"strconv"

	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
)

// handleListTools returns all registered tools.
// GET /api/tools
func (a *API) handleListTools(w http.ResponseWriter, r *http.Request) {
	if a.Services.ToolClient == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	tools := a.Services.ToolClient.ListTools()

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
	if a.Services.ToolClient == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	servers := a.Services.ToolClient.ListServers()
	a.jsonResp(w, http.StatusOK, servers)
}

// handleSelectTools previews tool selection for an intent.
// POST /api/tools/select
func (a *API) handleSelectTools(w http.ResponseWriter, r *http.Request) {
	if a.Services.ToolClient == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "tool client not configured")
		return
	}

	var req SelectToolsRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Intent == "" {
		req.Intent = "*"
	}

	// No per-session model context available at the API boundary — pass 0 so
	// SelectTools falls back to DefaultContextWindowTokens.
	tools, err := a.Services.ToolClient.SelectTools(r.Context(), req.Intent, req.Hints, "", "", 0)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, tools)
}

// handleRefreshTools triggers MCP tool discovery and syncs with skills table.
// POST /api/tools/refresh
func (a *API) handleRefreshTools(w http.ResponseWriter, r *http.Request) {
	if a.Services.MCP == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "MCP manager not configured")
		return
	}

	diff, err := a.Services.MCP.AutoDiscover(r.Context(), a.Services.Store)
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

	if a.Services.ToolClient == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	// Get all tools and filter by agent permissions.
	allTools := a.Services.ToolClient.ListTools()
	perms := a.Services.ToolClient.GetPermissions(agentID)

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

// handleListBrokerDecisions returns broker decision logs for a session.
// GET /api/broker/decisions?session_id=X&limit=N
func (a *API) handleListBrokerDecisions(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}

	limit := 50
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			limit = n
		}
	}

	decisions, err := a.Services.Store.ListBrokerDecisions(sessionID, limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, decisions)
}

// handleGetToolLoadPreferences returns the user's tool load type overrides.
// GET /api/tools/load-preferences
func (a *API) handleGetToolLoadPreferences(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Services.Store.GetUserSettings()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	prefs := settings.ToolLoadPreferences
	if prefs == nil {
		prefs = make(map[string]string)
	}
	a.jsonResp(w, http.StatusOK, prefs)
}

// handleUpdateToolLoadPreferences updates the user's tool load type overrides.
// PUT /api/tools/load-preferences
//
// Body: {"tool_name": "auto|opt-in", ...}
// Merges with existing preferences. Set a tool to "" to remove the override.
func (a *API) handleUpdateToolLoadPreferences(w http.ResponseWriter, r *http.Request) {
	var updates map[string]string
	if err := a.decode(r, &updates); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate values.
	for tool, lt := range updates {
		if lt != "" && lt != string(pluginpkg.LoadTypeAuto) && lt != string(pluginpkg.LoadTypeOptIn) {
			a.errorResp(w, http.StatusBadRequest, "invalid load_type for tool "+tool+": must be \"auto\", \"opt-in\", or \"\" (remove)")
			return
		}
	}

	settings, err := a.Services.Store.GetUserSettings()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	if settings.ToolLoadPreferences == nil {
		settings.ToolLoadPreferences = make(map[string]string)
	}

	for tool, lt := range updates {
		if lt == "" {
			delete(settings.ToolLoadPreferences, tool)
		} else {
			settings.ToolLoadPreferences[tool] = lt
		}
	}

	if err := a.Services.Store.UpdateUserSettings(settings); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, settings.ToolLoadPreferences)
}

// handleListToolsWithLoadType returns all discovered tools with their resolved loadType.
// GET /api/tools/all
func (a *API) handleListToolsWithLoadType(w http.ResponseWriter, r *http.Request) {
	if a.Services.MCP == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	// Get all tools regardless of loadType.
	allTools := a.Services.MCP.GetAllToolsUnfiltered()

	// Build the user-level override layer.
	settings, err := a.Services.Store.GetUserSettings()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	userOverrides := make(map[string]pluginpkg.LoadType, len(settings.ToolLoadPreferences))
	for k, v := range settings.ToolLoadPreferences {
		userOverrides[k] = pluginpkg.LoadType(v)
	}
	resolver := pluginpkg.NewLoadTypeResolver(
		pluginpkg.LoadTypeLayer{Name: "user", Overrides: userOverrides},
	)

	type toolLoadItem struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		LoadType    string `json:"load_type"`
		Source      string `json:"load_type_source"`
		Enabled     bool   `json:"enabled"`
	}

	items := make([]toolLoadItem, 0, len(allTools))
	for _, t := range allTools {
		lt, source := resolver.ResolveWithSource(t.Name)
		items = append(items, toolLoadItem{
			Name:        t.Name,
			Description: t.Description,
			LoadType:    string(lt),
			Source:      source,
			Enabled:     !lt.IsOptIn(),
		})
	}

	a.jsonResp(w, http.StatusOK, items)
}
