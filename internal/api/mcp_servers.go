package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// handleListMCPServers returns all persisted MCP server configs.
// GET /api/mcp-servers
func (a *API) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.Services.Store.ListMCPServers()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, servers)
}

// handleCreateMCPServer adds a new MCP server config and registers it.
// POST /api/mcp-servers
func (a *API) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	var cfg store.MCPServerConfig
	if err := a.decode(r, &cfg); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if cfg.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}
	if cfg.TransportType == "" {
		cfg.TransportType = "stdio"
	}
	if cfg.TransportType != "stdio" && cfg.TransportType != "sse" {
		a.errorResp(w, http.StatusBadRequest, "transport_type must be 'stdio' or 'sse'")
		return
	}

	// Check for duplicates.
	existing, _ := a.Services.Store.GetMCPServer(cfg.Name)
	if existing != nil {
		a.errorResp(w, http.StatusConflict, "server with this name already exists")
		return
	}

	cfg.Enabled = true
	if err := a.Services.Store.CreateMCPServer(&cfg); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Register the transport with the MCP manager.
	a.registerMCPTransport(&cfg)

	// Run discovery to pick up new tools.
	if a.Services.MCP != nil {
		a.Services.MCP.AutoDiscover(context.Background(), a.Services.Store)
	}

	a.jsonResp(w, http.StatusCreated, cfg)
}

// handleUpdateMCPServer updates an existing MCP server config.
// PUT /api/mcp-servers/{name}
func (a *API) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	existing, err := a.Services.Store.GetMCPServer(name)
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

	if cfg.TransportType == "" {
		cfg.TransportType = existing.TransportType
	}
	if cfg.TransportType != "stdio" && cfg.TransportType != "sse" {
		a.errorResp(w, http.StatusBadRequest, "transport_type must be 'stdio' or 'sse'")
		return
	}

	if err := a.Services.Store.UpdateMCPServer(&cfg); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Re-register: remove old transport and add new one.
	if a.Services.MCP != nil {
		a.Services.MCP.RemoveServer(name)
		a.registerMCPTransport(&cfg)
		a.Services.MCP.AutoDiscover(context.Background(), a.Services.Store)
	}

	a.jsonResp(w, http.StatusOK, cfg)
}

// handleDeleteMCPServer removes an MCP server config and unregisters it.
// DELETE /api/mcp-servers/{name}
func (a *API) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := a.Services.Store.DeleteMCPServer(name); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	// Unregister from MCP manager.
	if a.Services.MCP != nil {
		a.Services.MCP.RemoveServer(name)
	}

	w.WriteHeader(http.StatusNoContent)
}

// registerMCPTransport registers the transport for a server config with the MCP manager.
func (a *API) registerMCPTransport(cfg *store.MCPServerConfig) {
	if a.Services.MCP == nil || !cfg.Enabled {
		return
	}

	switch cfg.TransportType {
	case "stdio":
		var args []string
		if cfg.Args != "" && cfg.Args != "[]" {
			json.Unmarshal([]byte(cfg.Args), &args)
		}
		var env []string
		if cfg.Env != "" && cfg.Env != "[]" {
			json.Unmarshal([]byte(cfg.Env), &env)
		}
		a.Services.MCP.AddStdioServer(cfg.Name, cfg.Command, args, env)
	case "sse":
		a.Services.MCP.AddHTTPServer(cfg.Name, cfg.URL)
	}
}
