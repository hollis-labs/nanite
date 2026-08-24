package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpconfig"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListMCPServers returns all persisted MCP server configs.
// GET /api/mcp-servers
func (a *API) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.Services.Store.ListMCPServers(r.Context())
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
	existing, _ := a.Services.Store.GetMCPServer(r.Context(), cfg.Name)
	if existing != nil {
		a.errorResp(w, http.StatusConflict, "server with this name already exists")
		return
	}

	cfg.Enabled = true
	if err := a.Services.Store.CreateMCPServer(r.Context(), &cfg); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Register the transport with the MCP manager.
	a.registerMCPTransport(&cfg)

	// Run discovery to pick up new tools.
	if a.Services.MCP != nil {
		if _, err := a.Services.MCP.AutoDiscover(context.Background(), a.Services.Store); err != nil {
			slog.Warn("api: MCP tool discovery after create failed", "server", cfg.Name, "err", err)
		}
	}

	a.jsonResp(w, http.StatusCreated, cfg)
}

// handleUpdateMCPServer updates an existing MCP server config.
// PUT /api/mcp-servers/{name}
func (a *API) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	existing, err := a.Services.Store.GetMCPServer(r.Context(), name)
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

	if err := a.Services.Store.UpdateMCPServer(r.Context(), &cfg); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Re-register: remove old transport and add new one.
	if a.Services.MCP != nil {
		a.Services.MCP.RemoveServer(name)
		a.registerMCPTransport(&cfg)
		if _, err := a.Services.MCP.AutoDiscover(context.Background(), a.Services.Store); err != nil {
			slog.Warn("api: MCP tool discovery after update failed")
		}
	}

	a.jsonResp(w, http.StatusOK, cfg)
}

// handleDeleteMCPServer removes an MCP server config and unregisters it.
// DELETE /api/mcp-servers/{name}
func (a *API) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := a.Services.Store.DeleteMCPServer(r.Context(), name); err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	// Unregister from MCP manager.
	if a.Services.MCP != nil {
		a.Services.MCP.RemoveServer(name)
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

	result, err := mcpconfig.Import(a.Services.Store, body)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	// Register newly created servers with the MCP manager.
	for _, name := range result.Created {
		cfg, _ := a.Services.Store.GetMCPServer(r.Context(), name)
		if cfg != nil {
			a.registerMCPTransport(cfg)
		}
	}

	// Run discovery for new tools.
	if a.Services.MCP != nil && len(result.Created) > 0 {
		if _, err := a.Services.MCP.AutoDiscover(context.Background(), a.Services.Store); err != nil {
			slog.Warn("api: MCP tool discovery after import failed", "created", result.Created, "err", err)
		}
	}

	a.jsonResp(w, http.StatusOK, result)
}

// handleExportMCPServers exports all MCP server configs as a .mcp.json payload.
// GET /api/mcp-servers/export
func (a *API) handleExportMCPServers(w http.ResponseWriter, r *http.Request) {
	cfg, err := mcpconfig.Export(a.Services.Store)
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

// registerMCPTransport registers the transport for a server config with the MCP manager.
func (a *API) registerMCPTransport(cfg *store.MCPServerConfig) {
	if a.Services.MCP == nil || !cfg.Enabled {
		return
	}

	switch cfg.TransportType {
	case "stdio":
		args := decodeMCPStringSlice(cfg.Args, "args")
		env := decodeMCPStringSlice(cfg.Env, "env")
		envAllowlist := decodeMCPStringSlice(cfg.EnvAllowlist, "env_allowlist")
		if err := a.Services.MCP.AddStdioServer(cfg.Name, cfg.Command, args, env, envAllowlist, mcp.TrustTier(cfg.TrustTier)); err != nil {
			slog.Warn("api: failed to register stdio MCP server", "name", cfg.Name, "err", err)
		}
	case "sse":
		if err := a.Services.MCP.AddHTTPServer(cfg.Name, cfg.URL, mcp.TrustTier(cfg.TrustTier)); err != nil {
			slog.Warn("api: failed to register http MCP server", "name", cfg.Name, "err", err)
		}
	}
}

func decodeMCPStringSlice(raw, field string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		slog.Warn("api: malformed MCP string-list json — ignoring", "field", field)
		return nil
	}
	return values
}
