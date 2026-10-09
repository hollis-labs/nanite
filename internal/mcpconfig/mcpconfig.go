// Package mcpconfig provides import/export of MCP server configurations
// in the Claude Code .mcp.json format.
package mcpconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/hollis-labs/nanite/internal/store"
)

// ClaudeCodeConfig represents the top-level .mcp.json structure used by
// Claude Code and other MCP-aware tools.
type ClaudeCodeConfig struct {
	MCPServers map[string]ServerEntry `json:"mcpServers"`
}

// ServerEntry represents a single MCP server in the .mcp.json format.
type ServerEntry struct {
	// Type is "stdio" or "http" for imported operational configurations.
	// Export preserves "sse" on legacy rows for inspection and explicit migration;
	// importing that discriminator is refused without guessing a replacement URL.
	Type string `json:"type,omitempty"`

	// stdio transport
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// sse/http transport
	URL string `json:"url,omitempty"`

	// env is an object of key-value pairs in .mcp.json
	Env map[string]string `json:"env,omitempty"`
}

// ImportResult summarizes what happened during an import.
type ImportResult struct {
	Created []string `json:"created"`
	Skipped []string `json:"skipped"` // already existed
}

// Parse reads a .mcp.json byte slice and returns the config.
func Parse(data []byte) (*ClaudeCodeConfig, error) {
	var cfg ClaudeCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}
	if cfg.MCPServers == nil {
		return nil, fmt.Errorf("parse mcp config: missing mcpServers key")
	}
	// Validate the whole payload before Import creates any rows. An unknown
	// URL transport must not silently become HTTP, nor may a legacy endpoint
	// be persisted as a newly usable configuration.
	for name, entry := range cfg.MCPServers {
		transport := store.TransportStdio
		switch entry.Type {
		case store.TransportSSE:
			transport = store.TransportSSE
		case "", "http", store.TransportStdio:
			if entry.URL != "" {
				if entry.Type == store.TransportStdio {
					return nil, fmt.Errorf("parse mcp config: server %q: stdio cannot specify a URL", name)
				}
				transport = store.TransportStreamable
			} else if entry.Type == "http" {
				return nil, fmt.Errorf("parse mcp config: server %q: http requires a URL", name)
			}
		default:
			return nil, fmt.Errorf("parse mcp config: server %q: type must be 'stdio' or 'http'", name)
		}
		if err := ValidateTransport(transport, entry.URL); err != nil {
			return nil, fmt.Errorf("parse mcp config: server %q: %w", name, err)
		}
	}
	return &cfg, nil
}

// ToStoreConfigs converts a ClaudeCodeConfig into store-compatible records.
func ToStoreConfigs(cfg *ClaudeCodeConfig) []store.MCPServerConfig {
	out := make([]store.MCPServerConfig, 0, len(cfg.MCPServers))
	for name, entry := range cfg.MCPServers {
		sc := store.MCPServerConfig{
			Name:    name,
			Enabled: true,
		}

		if entry.URL != "" {
			// Parsed URL entries use the official Streamable HTTP transport.
			// Preserve an explicit legacy discriminator if this projection is
			// called directly; runtime registration refuses it as well.
			sc.TransportType = store.TransportStreamable
			if entry.Type == store.TransportSSE {
				sc.TransportType = store.TransportSSE
			}
			sc.URL = entry.URL
		} else {
			sc.TransportType = store.TransportStdio
			sc.Command = entry.Command
		}

		if len(entry.Args) > 0 {
			b, _ := json.Marshal(entry.Args)
			sc.Args = string(b)
		} else {
			sc.Args = "[]"
		}

		if len(entry.Env) > 0 {
			envSlice := make([]string, 0, len(entry.Env))
			for k, v := range entry.Env {
				envSlice = append(envSlice, k+"="+v)
			}
			sort.Strings(envSlice)
			b, _ := json.Marshal(envSlice)
			sc.Env = string(b)
		} else {
			sc.Env = "[]"
		}

		out = append(out, sc)
	}
	// Deterministic order for tests.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ImportStore is the store surface Import writes through.
type ImportStore interface {
	GetMCPServer(ctx context.Context, name string) (*store.MCPServerConfig, error)
	CreateMCPServer(ctx context.Context, cfg *store.MCPServerConfig) error
}

// ExportStore is the store surface Export reads through.
type ExportStore interface {
	ListMCPServers(ctx context.Context) ([]store.MCPServerConfig, error)
}

// Import parses .mcp.json data and creates DB records, skipping any that
// already exist by name. prepare, when non-nil, is applied to each new record
// before it is created.
func Import(ctx context.Context, s ImportStore, data []byte, prepare func(*store.MCPServerConfig)) (*ImportResult, error) {
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}

	configs := ToStoreConfigs(cfg)
	result := &ImportResult{}

	for i := range configs {
		existing, err := s.GetMCPServer(ctx, configs[i].Name)
		if err != nil {
			return nil, fmt.Errorf("check existing server %q: %w", configs[i].Name, err)
		}
		if existing != nil {
			result.Skipped = append(result.Skipped, configs[i].Name)
			continue
		}
		if prepare != nil {
			prepare(&configs[i])
		}
		if err := s.CreateMCPServer(ctx, &configs[i]); err != nil {
			return nil, fmt.Errorf("create server %q: %w", configs[i].Name, err)
		}
		result.Created = append(result.Created, configs[i].Name)
	}

	return result, nil
}

// Export reads all MCP server configs from the store and returns a
// ClaudeCodeConfig suitable for writing as .mcp.json.
func Export(ctx context.Context, s ExportStore) (*ClaudeCodeConfig, error) {
	servers, err := s.ListMCPServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("export mcp config: %w", err)
	}

	cfg := &ClaudeCodeConfig{
		MCPServers: make(map[string]ServerEntry, len(servers)),
	}

	for _, sc := range servers {
		entry := ServerEntry{}

		switch sc.TransportType {
		case store.TransportSSE:
			entry.Type = store.TransportSSE
			entry.URL = sc.URL
		case store.TransportStreamable:
			entry.Type = "http" // .mcp.json's Streamable HTTP discriminator
			entry.URL = sc.URL
		default: // stdio
			entry.Command = sc.Command
		}

		if sc.Args != "" && sc.Args != "[]" {
			if err := json.Unmarshal([]byte(sc.Args), &entry.Args); err != nil {
				slog.Warn("mcpconfig: malformed stored args json — exporting empty args", "server", sc.Name, "err", err)
				entry.Args = nil
			}
		}

		if sc.Env != "" && sc.Env != "[]" {
			var envSlice []string
			if err := json.Unmarshal([]byte(sc.Env), &envSlice); err != nil {
				slog.Warn("mcpconfig: malformed stored env json — exporting empty env", "server", sc.Name, "err", err)
				envSlice = nil
			}
			if len(envSlice) > 0 {
				entry.Env = make(map[string]string, len(envSlice))
				for _, pair := range envSlice {
					for j := 0; j < len(pair); j++ {
						if pair[j] == '=' {
							entry.Env[pair[:j]] = pair[j+1:]
							break
						}
					}
				}
			}
		}

		cfg.MCPServers[sc.Name] = entry
	}

	return cfg, nil
}

// Marshal serializes a ClaudeCodeConfig as indented JSON.
func Marshal(cfg *ClaudeCodeConfig) ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}
