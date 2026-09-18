package api

import (
	"net/http"

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

// handleListAgentTools returns tools available to a specific agent, with an
// `allowed` flag per tool.
// GET /api/agents/{id}/tools
//
// The `allowed` computation mirrors internal/service/tool.go's
// SelectForAgent/filterToolsByAgentTools and
// internal/service/tool_execution_rules.go's
// enforceExecutionRulesViaAgentTools (TASKS/phase-4/05, TASKS/phase-5/01):
// agent_tools (+ the known_tools.always_included escape hatch) is the sole,
// unconditional gate for every agentID --
// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md removed
// the legacy tool_permissions/CheckPermission fallback that used to apply
// here for an agentID with no real agent_profiles row (only ever a
// file-based agent, eliminated by
// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md). A genuinely
// unknown agentID now simply reads back zero agent_tools grants (fail
// closed), not "everything allowed."
//
// This was the third and, per TASKS/phase-5/10's own audit, final
// operator-facing read surface still reporting the stale pre-agent_tools
// answer -- TASKS/phase-5/10-fix-list-agent-tools-endpoint-stale-
// permissions-view.md's Work Log has the live-dogfeed gap that found it.
func (a *API) handleListAgentTools(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	if a.Services.ToolClient == nil {
		a.jsonResp(w, http.StatusOK, []any{})
		return
	}

	allTools := a.Services.ToolClient.ListTools()

	type toolItem struct {
		// ID is the known_tools row ID a caller must pass as
		// GrantAgentToolRequest.ToolID to actually grant this tool --
		// resolved by name below and left empty when the tool hasn't been
		// synced into known_tools yet (SyncKnownTools runs at boot; see
		// docs/adding-an-agent.md's "known_tools learns MCP tools only at
		// boot" note). A UI should treat an empty ID as "not grantable
		// until a restart."
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Allowed     bool   `json:"allowed"`
	}

	items := make([]toolItem, 0, len(allTools))

	granted := make(map[string]bool)
	always := make(map[string]bool)
	knownIDs := make(map[string]string)
	if a.Services.Store != nil {
		if names, err := a.Services.Store.ListAgentToolNames(r.Context(), agentID); err == nil {
			for _, n := range names {
				granted[n] = true
			}
		}
		if rows, err := a.Services.Store.ListKnownTools(r.Context()); err == nil {
			for _, kt := range rows {
				knownIDs[kt.Name] = kt.ID
			}
		}
		// known_tools.always_included escape hatch (request_tools/
		// tool_list/tool_describe) -- must read as allowed regardless of
		// agent_tools grant membership, mirroring
		// resolveAlwaysIncludedTools/enforceExecutionRulesViaAgentTools.
		if rows, err := a.Services.Store.ListAlwaysIncludedKnownTools(r.Context()); err == nil {
			for _, kt := range rows {
				if kt.Status == "available" {
					always[kt.Name] = true
				}
			}
		}
	}
	for _, t := range allTools {
		items = append(items, toolItem{
			ID:          knownIDs[t.Name],
			Name:        t.Name,
			Description: t.Description,
			Allowed:     granted[t.Name] || always[t.Name],
		})
	}

	a.jsonResp(w, http.StatusOK, items)
}

// handleGetToolLoadPreferences returns the user's tool load type overrides.
// GET /api/tools/load-preferences
func (a *API) handleGetToolLoadPreferences(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Services.Store.GetUserSettings(r.Context())
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

	settings, err := a.Services.Store.GetUserSettings(r.Context())
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

	if err := a.Services.Store.UpdateUserSettings(r.Context(), settings); err != nil {
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
	settings, err := a.Services.Store.GetUserSettings(r.Context())
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
