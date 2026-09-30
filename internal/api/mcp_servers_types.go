package api

import (
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// MCPServerView is an MCP server config as the API returns it, from the
// list, create and update endpoints. Header values are always redacted
// (service.RedactHeaders); the keys survive. Field order is the store row's.
type MCPServerView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	TransportType string `json:"transport_type"`
	Command       string `json:"command"`
	URL           string `json:"url"`
	Args          string `json:"args"`
	Env           string `json:"env"`
	Enabled       bool   `json:"enabled"`
	TrustTier     string `json:"trust_tier"`
	EnvAllowlist  string `json:"env_allowlist"`
	Headers       string `json:"headers"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func mcpServerToView(cfg *store.MCPServerConfig) MCPServerView {
	return MCPServerView{
		ID:            cfg.ID,
		Name:          cfg.Name,
		TransportType: cfg.TransportType,
		Command:       cfg.Command,
		URL:           cfg.URL,
		Args:          cfg.Args,
		Env:           cfg.Env,
		Enabled:       cfg.Enabled,
		TrustTier:     cfg.TrustTier,
		EnvAllowlist:  cfg.EnvAllowlist,
		Headers:       service.RedactHeaders(cfg.Headers),
		CreatedAt:     cfg.CreatedAt,
		UpdatedAt:     cfg.UpdatedAt,
	}
}

// mcpServersToView never returns nil, so an empty list encodes as [].
func mcpServersToView(servers []store.MCPServerConfig) []MCPServerView {
	out := make([]MCPServerView, len(servers))
	for i := range servers {
		out[i] = mcpServerToView(&servers[i])
	}
	return out
}
