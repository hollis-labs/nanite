package api

import (
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// MCPServerView is an MCP server config as the API returns it, from the
// list, create and update endpoints. Header and env values are always
// redacted (service.RedactHeaders, service.RedactEnv); the header keys and
// env variable names survive. Field order is the store row's.
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
		Env:           service.RedactEnv(cfg.Env),
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

// UpdateMCPServerRequest is the body of PUT /api/mcp-servers/{name}. A field
// the body omits keeps its stored value; a field it sends replaces it, and an
// empty string clears it. An explicit JSON null decodes the same as omitted
// and also keeps the stored value. Header values sent back as the redaction
// placeholder, and env entries sent back as KEY=<placeholder>, keep their
// stored values. name, id, created_at and updated_at
// are not settable here and are ignored if sent.
type UpdateMCPServerRequest struct {
	TransportType *string `json:"transport_type"`
	Command       *string `json:"command"`
	URL           *string `json:"url"`
	Args          *string `json:"args"`
	Env           *string `json:"env"`
	Enabled       *bool   `json:"enabled"`
	TrustTier     *string `json:"trust_tier"`
	EnvAllowlist  *string `json:"env_allowlist"`
	Headers       *string `json:"headers"`
}

func (r UpdateMCPServerRequest) toPatch() service.MCPServerPatch {
	return service.MCPServerPatch{
		TransportType: r.TransportType,
		Command:       r.Command,
		URL:           r.URL,
		Args:          r.Args,
		Env:           r.Env,
		Enabled:       r.Enabled,
		TrustTier:     r.TrustTier,
		EnvAllowlist:  r.EnvAllowlist,
		Headers:       r.Headers,
	}
}

// CreateMCPServerRequest preserves the create contract without decoding a storage row.
type CreateMCPServerRequest struct {
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

func (r CreateMCPServerRequest) toStore() store.MCPServerConfig {
	return store.MCPServerConfig{
		ID:            r.ID,
		Name:          r.Name,
		TransportType: r.TransportType,
		Command:       r.Command,
		URL:           r.URL,
		Args:          r.Args,
		Env:           r.Env,
		Enabled:       r.Enabled,
		TrustTier:     r.TrustTier,
		EnvAllowlist:  r.EnvAllowlist,
		Headers:       r.Headers,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}
