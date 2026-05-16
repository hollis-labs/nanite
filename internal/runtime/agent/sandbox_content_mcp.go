package agent

import (
	"encoding/json"
	"fmt"
)

// renderMCPJSON returns the .mcp.json body for cfg + sessionID, or an
// empty string when MCP planting is disabled (zero-value cfg.DBPath).
//
// The descriptor names the nanite binary and the per-session args
// ("mcp --db <db> --session <sessID>"); claude / codex / opencode
// discover it from cwd and spawn the subprocess on tool-call. Nanite's
// MCP transport is subprocess-spawn-based — distinct from go-providers'
// loopback-URL renderMCPJSON, which is why Nanite keeps its own renderer
// (CW-20260515-0025: app-specific MCP shape, kept Nanite-side).
//
// Returns an error when cfg is internally inconsistent (BinaryPath is
// required once DBPath is set).
func renderMCPJSON(cfg MCPConfig, sessionID string) (string, error) {
	if cfg.DBPath == "" {
		return "", nil
	}
	if cfg.BinaryPath == "" {
		return "", fmt.Errorf("agent: MCPConfig.BinaryPath is required when DBPath is set")
	}

	serverID := cfg.ServerID
	if serverID == "" {
		serverID = "nanite"
	}

	args := []string{"mcp", "--db", cfg.DBPath}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}

	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			serverID: map[string]any{
				"command": cfg.BinaryPath,
				"args":    args,
				"env":     map[string]any{},
			},
		},
	}

	data, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent: marshal .mcp.json: %w", err)
	}
	return string(data), nil
}
