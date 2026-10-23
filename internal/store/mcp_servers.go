package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

// Transport-type values for MCPServerConfig.TransportType.
//
// TransportStreamable selects official SDK Streamable HTTP; TransportStdio
// selects a subprocess. TransportSSE is retained solely to read/export old rows
// and refuse their activation with explicit migration guidance. No migration
// rewrites a legacy endpoint or grants a transport access to additional tools.
const (
	TransportStdio      = "stdio"
	TransportSSE        = "sse"
	TransportStreamable = "streamable"
)

// MCPServerConfig represents a persisted MCP server configuration.
type MCPServerConfig struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// TransportType is one of the Transport* constants.
	TransportType string `json:"transport_type"`
	Command       string `json:"command"` // for stdio
	URL           string `json:"url"`     // for sse/streamable
	Args          string `json:"args"`    // JSON array of strings
	Env           string `json:"env"`     // JSON array of "KEY=VALUE" strings
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
	// Headers is a JSON object of static request headers sent with every
	// HTTP request to this server, e.g. {"Authorization": "Bearer ..."}.
	// Ignored by the stdio transport. Holds credentials: redact at the API
	// boundary, never in the store, which is the record of truth.
	Headers   string `json:"headers"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const mcpServerColumns = `id, name, transport_type, command, url, args, env, enabled,
	trust_tier, env_allowlist, headers, created_at, updated_at`

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
			&cfg.TrustTier, &cfg.EnvAllowlist, &cfg.Headers,
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
		&cfg.TrustTier, &cfg.EnvAllowlist, &cfg.Headers,
		&cfg.CreatedAt, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp server %s: %w", name, err)
	}
	return &cfg, nil
}

// ErrTetherGatewayConflict reports a second Tether messaging gateway.
var ErrTetherGatewayConflict = errors.New("a Tether messaging gateway is already registered")

// isTetherGateway reports whether cfg runs `tether ... --token-file <path>`:
// a Tether gateway that authenticates as one actor.
func isTetherGateway(cfg *MCPServerConfig) bool {
	if filepath.Base(cfg.Command) != "tether" {
		return false
	}
	var args []string
	if json.Unmarshal([]byte(cfg.Args), &args) != nil {
		return false
	}
	for _, a := range args {
		if a == "--token-file" || strings.HasPrefix(a, "--token-file=") {
			return true
		}
	}
	return false
}

// tetherGatewayFlag is the value of mcp_servers.tether_gateway for cfg. A
// unique partial index over the flag makes "one Tether actor per host" the
// database's decision: the gateway entry is host-wide, so each agent that
// lists it launches with the same token, and a second entry under another name
// would present a second actor's credential to the same agents. Create and
// update both write the flag in the same statement as the row, so concurrent
// writers on any connection cannot both succeed. Per-agent planting is what
// would lift this.
func tetherGatewayFlag(cfg *MCPServerConfig) int {
	if isTetherGateway(cfg) {
		return 1
	}
	return 0
}

// tetherGatewayConflict maps the index's violation to the typed error and
// names the gateway already registered, best effort.
func (s *Store) tetherGatewayConflict(ctx context.Context, err error, cfg *MCPServerConfig) error {
	if err == nil || !strings.Contains(err.Error(), "mcp_servers.tether_gateway") {
		return err
	}
	var other string
	_ = s.DB.QueryRowContext(ctx, `SELECT name FROM mcp_servers WHERE tether_gateway = 1 AND name != ? LIMIT 1`, cfg.Name).Scan(&other)
	return fmt.Errorf("%w as %q; one Tether actor per host is supported, remove it before registering %q", ErrTetherGatewayConflict, other, cfg.Name)
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
	if cfg.Headers == "" {
		cfg.Headers = "{}"
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO mcp_servers (`+mcpServerColumns+`, tether_gateway)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.ID, cfg.Name, cfg.TransportType, cfg.Command, cfg.URL,
		cfg.Args, cfg.Env, cfg.Enabled,
		cfg.TrustTier, cfg.EnvAllowlist, cfg.Headers,
		now, now, tetherGatewayFlag(cfg),
	)
	if err = s.tetherGatewayConflict(ctx, err, cfg); err != nil {
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
	if cfg.Headers == "" {
		cfg.Headers = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE mcp_servers SET transport_type = ?, command = ?, url = ?, args = ?, env = ?,
			enabled = ?, trust_tier = ?, env_allowlist = ?, headers = ?, updated_at = ?,
			tether_gateway = ?
		 WHERE name = ?`,
		cfg.TransportType, cfg.Command, cfg.URL, cfg.Args, cfg.Env, cfg.Enabled,
		cfg.TrustTier, cfg.EnvAllowlist, cfg.Headers, now, tetherGatewayFlag(cfg), cfg.Name,
	)
	if err = s.tetherGatewayConflict(ctx, err, cfg); err != nil {
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
