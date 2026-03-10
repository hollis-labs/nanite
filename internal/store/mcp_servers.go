package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MCPServerConfig represents a persisted MCP server configuration.
type MCPServerConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	TransportType string `json:"transport_type"` // "stdio" or "sse"
	Command       string `json:"command"`         // for stdio
	URL           string `json:"url"`             // for sse/http
	Args          string `json:"args"`            // JSON array of strings
	Env           string `json:"env"`             // JSON array of "KEY=VALUE" strings
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// ListMCPServers returns all MCP server configs ordered by name.
func (s *Store) ListMCPServers() ([]MCPServerConfig, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, transport_type, command, url, args, env, enabled, created_at, updated_at
		 FROM mcp_servers ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	defer rows.Close()

	out := make([]MCPServerConfig, 0)
	for rows.Next() {
		var cfg MCPServerConfig
		if err := rows.Scan(&cfg.ID, &cfg.Name, &cfg.TransportType, &cfg.Command, &cfg.URL,
			&cfg.Args, &cfg.Env, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan mcp server: %w", err)
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// GetMCPServer returns an MCP server config by name.
func (s *Store) GetMCPServer(name string) (*MCPServerConfig, error) {
	var cfg MCPServerConfig
	err := s.DB.QueryRow(
		`SELECT id, name, transport_type, command, url, args, env, enabled, created_at, updated_at
		 FROM mcp_servers WHERE name = ?`, name,
	).Scan(&cfg.ID, &cfg.Name, &cfg.TransportType, &cfg.Command, &cfg.URL,
		&cfg.Args, &cfg.Env, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp server %s: %w", name, err)
	}
	return &cfg, nil
}

// CreateMCPServer inserts a new MCP server config.
func (s *Store) CreateMCPServer(cfg *MCPServerConfig) error {
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

	_, err := s.DB.Exec(
		`INSERT INTO mcp_servers (id, name, transport_type, command, url, args, env, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.ID, cfg.Name, cfg.TransportType, cfg.Command, cfg.URL,
		cfg.Args, cfg.Env, cfg.Enabled, now, now,
	)
	if err != nil {
		return fmt.Errorf("create mcp server: %w", err)
	}
	cfg.CreatedAt = now
	cfg.UpdatedAt = now
	return nil
}

// UpdateMCPServer updates an MCP server config by name.
func (s *Store) UpdateMCPServer(cfg *MCPServerConfig) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE mcp_servers SET transport_type = ?, command = ?, url = ?, args = ?, env = ?, enabled = ?, updated_at = ?
		 WHERE name = ?`,
		cfg.TransportType, cfg.Command, cfg.URL, cfg.Args, cfg.Env, cfg.Enabled, now, cfg.Name,
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
func (s *Store) DeleteMCPServer(name string) error {
	res, err := s.DB.Exec(`DELETE FROM mcp_servers WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete mcp server %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mcp server %q not found", name)
	}
	return nil
}
