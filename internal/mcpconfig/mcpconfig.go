// Package mcpconfig provides import/export of MCP server configurations
// in the Claude Code .mcp.json format.
package mcpconfig

import (
	"context"
	"encoding/json"
	"fmt"
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
	// stdio transport
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// sse/http transport
	URL string `json:"url,omitempty"`

	// env is an object of key-value pairs in .mcp.json
	Env map[string]string `json:"env,omitempty"`
}

// ImportResult summarises what happened during an import.
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
			sc.TransportType = "sse"
			sc.URL = entry.URL
		} else {
			sc.TransportType = "stdio"
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

// Import parses .mcp.json data and creates DB records, skipping any that
// already exist by name.
func Import(s *store.Store, data []byte) (*ImportResult, error) {
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}

	configs := ToStoreConfigs(cfg)
	result := &ImportResult{}

	for i := range configs {
		existing, err := s.GetMCPServer(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, configs[i].Name)
		if err != nil {
			return nil, fmt.Errorf("check existing server %q: %w", configs[i].Name, err)
		}
		if existing != nil {
			result.Skipped = append(result.Skipped, configs[i].Name)
			continue
		}
		if err := s.CreateMCPServer(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, &configs[i]); err != nil {
			return nil, fmt.Errorf("create server %q: %w", configs[i].Name, err)
		}
		result.Created = append(result.Created, configs[i].Name)
	}

	return result, nil
}

// Export reads all MCP server configs from the store and returns a
// ClaudeCodeConfig suitable for writing as .mcp.json.
func Export(s *store.Store) (*ClaudeCodeConfig, error) {
	servers, err := s.ListMCPServers(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
	if err != nil {
		return nil, fmt.Errorf("export mcp config: %w", err)
	}

	cfg := &ClaudeCodeConfig{
		MCPServers: make(map[string]ServerEntry, len(servers)),
	}

	for _, sc := range servers {
		entry := ServerEntry{}

		switch sc.TransportType {
		case "sse":
			entry.URL = sc.URL
		default: // stdio
			entry.Command = sc.Command
		}

		if sc.Args != "" && sc.Args != "[]" {
			json.Unmarshal([]byte(sc.Args), &entry.Args)
		}

		if sc.Env != "" && sc.Env != "[]" {
			var envSlice []string
			json.Unmarshal([]byte(sc.Env), &envSlice)
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

// Marshal serialises a ClaudeCodeConfig as indented JSON.
func Marshal(cfg *ClaudeCodeConfig) ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}
