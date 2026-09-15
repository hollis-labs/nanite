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

// RedactedHeaderValue replaces a stored header value on the way out.
//
// The key survives so the UI can show that an Authorization header exists
// without showing the token, and a client that sends this value back on an
// update is understood to mean "leave it alone" — see mergeRedactedHeaders.
const RedactedHeaderValue = "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"

// redactHeaders blanks header values for a response. Headers hold credentials
// — a bearer token for a gateway, most often — and this endpoint feeds the UI,
// so the values must not travel with it.
//
// Operates on a copy: the caller's configs are also what the register path
// reads, and redacting in place would register servers with a token of bullets.
func redactHeaders(servers []store.MCPServerConfig) []store.MCPServerConfig {
	out := make([]store.MCPServerConfig, len(servers))
	copy(out, servers)
	for i := range out {
		parsed, err := mcp.ParseHeaderJSON(out[i].Headers)
		if err != nil || len(parsed) == 0 {
			// Unparseable is not shown either: it may still hold a secret.
			out[i].Headers = "{}"
			continue
		}
		masked := make(map[string]string, len(parsed))
		for k := range parsed {
			masked[k] = RedactedHeaderValue
		}
		encoded, err := json.Marshal(masked)
		if err != nil {
			out[i].Headers = "{}"
			continue
		}
		out[i].Headers = string(encoded)
	}
	return out
}

// mergeRedactedHeaders restores values the client sent back redacted.
//
// Without this the first save from a UI that loaded the list would overwrite a
// working token with a row of bullets, and the server would start returning
// 401s with nothing in the audit trail to explain why. A redacted value means
// "unchanged"; any other value, including an empty one, is a deliberate edit.
func mergeRedactedHeaders(incoming, stored string) string {
	in, err := mcp.ParseHeaderJSON(incoming)
	if err != nil || len(in) == 0 {
		return incoming
	}
	old, err := mcp.ParseHeaderJSON(stored)
	if err != nil || len(old) == 0 {
		return incoming
	}
	changed := false
	for k, v := range in {
		if v != RedactedHeaderValue {
			continue
		}
		if prev, ok := old[k]; ok {
			in[k] = prev
			changed = true
		} else {
			delete(in, k)
			changed = true
		}
	}
	if !changed {
		return incoming
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return incoming
	}
	return string(encoded)
}

// handleListMCPServers returns all persisted MCP server configs, with header
// values redacted.
// GET /api/mcp-servers
func (a *API) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.Services.Store.ListMCPServers(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, redactHeaders(servers))
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
		cfg.TransportType = store.TransportStdio
	}
	if !validTransportType(cfg.TransportType) {
		a.errorResp(w, http.StatusBadRequest, transportTypeError)
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

	// A value the client sends back still redacted means "unchanged", not
	// "set it to bullets". Without this a save from a UI that loaded the
	// redacted list would quietly replace a working token.
	cfg.Headers = mergeRedactedHeaders(cfg.Headers, existing.Headers)

	if cfg.TransportType == "" {
		cfg.TransportType = existing.TransportType
	}
	if !validTransportType(cfg.TransportType) {
		a.errorResp(w, http.StatusBadRequest, transportTypeError)
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
			// #nosec G706 -- server and err are structured operational diagnostics, not a formatted log message.
			slog.Warn("api: MCP tool discovery after update failed", "server", cfg.Name, "err", err)
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

// transportTypeError is the 400 body for an unrecognized transport_type. It
// names all three because "sse" and "streamable" are easy to pick wrongly:
// "sse" is the 2024-11-05 HTTP+SSE transport, "streamable" is JSON-RPC over
// POST — which is what a ContextForge /mcp URL speaks.
const transportTypeError = "transport_type must be 'stdio', 'sse', or 'streamable'"

func validTransportType(t string) bool {
	switch t {
	case store.TransportStdio, store.TransportSSE, store.TransportStreamable:
		return true
	default:
		return false
	}
}

// registerMCPTransport registers the transport for a server config with the MCP manager.
func (a *API) registerMCPTransport(cfg *store.MCPServerConfig) {
	if a.Services.MCP == nil || !cfg.Enabled {
		return
	}

	switch cfg.TransportType {
	case store.TransportStdio:
		args := decodeMCPStringSlice(cfg.Args, cfg.Name, "args")
		env := decodeMCPStringSlice(cfg.Env, cfg.Name, "env")
		envAllowlist := decodeMCPStringSlice(cfg.EnvAllowlist, cfg.Name, "env_allowlist")
		if err := a.Services.MCP.AddStdioServer(cfg.Name, cfg.Command, args, env, envAllowlist, mcp.TrustTier(cfg.TrustTier)); err != nil {
			slog.Warn("api: failed to register stdio MCP server", "name", cfg.Name, "err", err)
		}
	default:
		if err := a.Services.MCP.AddRemoteServerFromConfig(cfg.Name, cfg.TransportType, cfg.URL, cfg.Headers, mcp.TrustTier(cfg.TrustTier)); err != nil {
			// #nosec G706 -- name, transport, and err are structured operational diagnostics, not a formatted log message.
			slog.Warn("api: failed to register remote MCP server",
				"name", cfg.Name, "transport", cfg.TransportType, "err", err)
		}
	}
}

func decodeMCPStringSlice(raw, server, field string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		// #nosec G706 -- server, field, and err are structured operational diagnostics, not a formatted log message.
		slog.Warn("api: malformed MCP string-list json — ignoring", "server", server, "field", field, "err", err)
		return nil
	}
	return values
}
