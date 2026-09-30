package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/nanite/internal/mcpconfig"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListMCPServers returns all persisted MCP server configs, with header
// values redacted.
// GET /api/mcp-servers
func (a *API) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.Services.MCPServers.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, mcpServersToView(servers))
}

// handleCreateMCPServer adds a new MCP server config and registers it.
// POST /api/mcp-servers
func (a *API) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	var cfg store.MCPServerConfig
	if err := a.decode(r, &cfg); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := a.Services.MCPServers.Create(r.Context(), &cfg); err != nil {
		var ve *service.MCPServerValidationError
		switch {
		case errors.As(err, &ve):
			a.errorResp(w, http.StatusBadRequest, ve.Msg)
		case errors.Is(err, service.ErrMCPServerExists):
			a.errorResp(w, http.StatusConflict, err.Error())
		default:
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	a.jsonResp(w, http.StatusCreated, mcpServerToView(&cfg))
}

// handleUpdateMCPServer updates an existing MCP server config.
// PUT /api/mcp-servers/{name}
func (a *API) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	existing, err := a.Services.MCPServers.Get(r.Context(), name)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "server not found")
		return
	}

	var cfg store.MCPServerConfig
	if err := a.decode(r, &cfg); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Preserve the name from the URL path.
	cfg.Name = name
	cfg.ID = existing.ID

	if err := a.Services.MCPServers.Update(r.Context(), existing, &cfg); err != nil {
		var ve *service.MCPServerValidationError
		if errors.As(err, &ve) {
			a.errorResp(w, http.StatusBadRequest, ve.Msg)
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, mcpServerToView(&cfg))
}

// handleDeleteMCPServer removes an MCP server config and unregisters it.
// DELETE /api/mcp-servers/{name}
func (a *API) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := a.Services.MCPServers.Delete(r.Context(), name); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleImportMCPServers imports MCP server configs from a .mcp.json payload.
// POST /api/mcp-servers/import
func (a *API) handleImportMCPServers(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	result, err := a.Services.MCPServers.Import(r.Context(), body)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, result)
}

// handleExportMCPServers exports all MCP server configs as a .mcp.json payload.
// GET /api/mcp-servers/export
func (a *API) handleExportMCPServers(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.Services.MCPServers.Export(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	data, err := mcpconfig.Marshal(cfg)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename=".mcp.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // The response is already committed; a client disconnect has no recovery response path.
}
