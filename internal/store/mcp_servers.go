package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Trust-tier values mirrored from internal/mcp.TrustTier. Duplicated here as
// strings to keep the store layer free of an mcp-package import. The mcp
// package imports store, not the other way around.
const (
	TrustTierBuiltin        = "builtin"
	TrustTierPluginStdio    = "plugin_stdio"
	TrustTierPluginHTTP     = "plugin_http"
	TrustTierThirdPartyHTTP = "third_party_http"
)

// MCPServerConfig represents a persisted MCP server configuration.
type MCPServerConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	TransportType string `json:"transport_type"` // "stdio" or "sse"
	Command       string `json:"command"`        // for stdio
	URL           string `json:"url"`            // for sse/http
	Args          string `json:"args"`           // JSON array of strings
	Env           string `json:"env"`            // JSON array of "KEY=VALUE" strings
	Enabled       bool   `json:"enabled"`
	// TrustTier is one of TrustTier* constants. Persisted rows are by
	// definition user/catalog-sourced, so the default is third_party_http
	// (D4 fail-closed). Built-in and plugin-registered servers carry their
	// tier at runtime via the Manager registration API.
	TrustTier string `json:"trust_tier"`
	// EnvAllowlist is a JSON array of env-var names that the stdio
	// transport may inherit when spawning the subprocess. Empty array
	// means nothing is inherited.
	EnvAllowlist string `json:"env_allowlist"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

const mcpServerColumns = `id, name, transport_type, command, url, args, env, enabled,
	trust_tier, env_allowlist, created_at, updated_at`

// ListMCPServers returns all MCP server configs ordered by name.
func (s *Store) ListMCPServers(ctx context.Context) ([]MCPServerConfig, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+mcpServerColumns+` FROM mcp_servers ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	defer closeRows(rows)

	out := make([]MCPServerConfig, 0)
	for rows.Next() {
		var cfg MCPServerConfig
		if err := rows.Scan(&cfg.ID, &cfg.Name, &cfg.TransportType, &cfg.Command, &cfg.URL,
			&cfg.Args, &cfg.Env, &cfg.Enabled,
			&cfg.TrustTier, &cfg.EnvAllowlist,
			&cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mcp server: %w", err)
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// GetMCPServer returns an MCP server config by name.
func (s *Store) GetMCPServer(ctx context.Context, name string) (*MCPServerConfig, error) {
	var cfg MCPServerConfig
	err := s.DB.QueryRowContext(ctx,
		`SELECT `+mcpServerColumns+` FROM mcp_servers WHERE name = ?`, name,
	).Scan(&cfg.ID, &cfg.Name, &cfg.TransportType, &cfg.Command, &cfg.URL,
		&cfg.Args, &cfg.Env, &cfg.Enabled,
		&cfg.TrustTier, &cfg.EnvAllowlist,
		&cfg.CreatedAt, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp server %s: %w", name, err)
	}
	return &cfg, nil
}

// CreateMCPServer inserts a new MCP server config.
func (s *Store) CreateMCPServer(ctx context.Context, cfg *MCPServerConfig) error {
	if cfg.ID == "" {
		cfg.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if cfg.Args == "" {
		cfg.Args = "[]"
	}
	if cfg.Env == "" {
		cfg.Env = "[]"
	}
	if cfg.TrustTier == "" {
		cfg.TrustTier = TrustTierThirdPartyHTTP
	}
	if cfg.EnvAllowlist == "" {
		cfg.EnvAllowlist = "[]"
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO mcp_servers (`+mcpServerColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.ID, cfg.Name, cfg.TransportType, cfg.Command, cfg.URL,
		cfg.Args, cfg.Env, cfg.Enabled,
		cfg.TrustTier, cfg.EnvAllowlist,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("create mcp server: %w", err)
	}
	cfg.CreatedAt = now
	cfg.UpdatedAt = now
	return nil
}

// UpdateMCPServer updates an MCP server config by name.
func (s *Store) UpdateMCPServer(ctx context.Context, cfg *MCPServerConfig) error {
	if cfg.TrustTier == "" {
		cfg.TrustTier = TrustTierThirdPartyHTTP
	}
	if cfg.EnvAllowlist == "" {
		cfg.EnvAllowlist = "[]"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE mcp_servers SET transport_type = ?, command = ?, url = ?, args = ?, env = ?,
			enabled = ?, trust_tier = ?, env_allowlist = ?, updated_at = ?
		 WHERE name = ?`,
		cfg.TransportType, cfg.Command, cfg.URL, cfg.Args, cfg.Env, cfg.Enabled,
		cfg.TrustTier, cfg.EnvAllowlist, now, cfg.Name,
	)
	if err != nil {
		return fmt.Errorf("update mcp server: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mcp server %q not found", cfg.Name)
	}
	cfg.UpdatedAt = now
	return nil
}

// DeleteMCPServer removes an MCP server config by name.
func (s *Store) DeleteMCPServer(ctx context.Context, name string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM mcp_servers WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete mcp server %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mcp server %q not found", name)
	}
	return nil
}
