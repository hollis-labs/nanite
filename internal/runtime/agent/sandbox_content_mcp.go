package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/fsutil"
)

// writeMCPJSON plants .mcp.json under dir using cfg + sessionID. The file
// names the nanite binary and the per-session args
// ("mcp --db <db> --session <sessID>"); claude / codex / opencode discover
// it from cwd and spawn the subprocess on tool-call.
//
// Zero-value cfg.DBPath disables MCP planting (some tests / standalone
// runs). Empty cfg.ServerID falls back to "nanite".
func writeMCPJSON(dir string, cfg MCPConfig, sessionID string) error {
	if cfg.DBPath == "" {
		return nil
	}
	if cfg.BinaryPath == "" {
		return fmt.Errorf("agent: MCPConfig.BinaryPath is required when DBPath is set")
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
		return fmt.Errorf("agent: marshal .mcp.json: %w", err)
	}

	if err := fsutil.AtomicWriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644); err != nil {
		return fmt.Errorf("agent: write .mcp.json: %w", err)
	}
	return nil
}
