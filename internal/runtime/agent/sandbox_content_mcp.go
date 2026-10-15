package agent

import (
	"encoding/json"
	"fmt"
)

// SelfToolsScopeEnv is planted into a launch's .mcp.json next to
// NANITE_API_URL to name the self tools its `nanite mcp` advertises. Unset
// is every self tool, a chat launch's surface. SelfToolsScopeStore is only
// the set a bare store serves, for subagent, background and one-shot
// launches (CW-20261001-0188).
const SelfToolsScopeEnv = "NANITE_MCP_SELF_TOOLS"

// SelfToolsScopeStore is SelfToolsScopeEnv's value for the bare-store set.
const SelfToolsScopeStore = "store"

// AuthTokenEnv names the credential the spawned `nanite mcp` presents to the
// API (the same variable the CLI reads).
const AuthTokenEnv = "NANITE_AUTH_TOKEN" // #nosec G101 -- an environment variable name, not a credential

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
// storeScope plants SelfToolsScopeEnv=SelfToolsScopeStore with the API URL.
//
// Returns an error when cfg is internally inconsistent (BinaryPath is
// required once DBPath is set).
func renderMCPJSON(cfg MCPConfig, sessionID string, storeScope bool) (string, error) {
	id, command, args, env, err := naniteMCPEntry(cfg, sessionID, storeScope)
	if err != nil || id == "" {
		return "", err
	}
	envAny := make(map[string]any, len(env))
	for k, v := range env {
		envAny[k] = v
	}
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			id: map[string]any{
				"command": command,
				"args":    args,
				"env":     envAny,
			},
		},
	}
	data, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent: marshal .mcp.json: %w", err)
	}
	return string(data), nil
}

// naniteMCPEntry is the one description of the Nanite MCP server a launch
// plants, in every provider's file format: the server ID, the binary and argv
// (`mcp --db <db> --session <id>`), and the environment that points it at the
// live API with a session credential. The ID is empty when MCP planting is
// disabled (zero-value cfg.DBPath).
func naniteMCPEntry(cfg MCPConfig, sessionID string, storeScope bool) (id, command string, args []string, env map[string]string, err error) {
	if cfg.DBPath == "" {
		return "", "", nil, nil, nil
	}
	if cfg.BinaryPath == "" {
		return "", "", nil, nil, fmt.Errorf("agent: MCPConfig.BinaryPath is required when DBPath is set")
	}
	args = []string{"mcp", "--db", cfg.DBPath}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	// When the composition root knows the live API server's address, plant it
	// as NANITE_API_URL so the subprocess forwards self-tool calls to the
	// running harness and opens no database (Option A; CW-20261001-0188).
	env = map[string]string{}
	if cfg.APIBaseURL != "" {
		env["NANITE_API_URL"] = cfg.APIBaseURL
		if storeScope {
			env[SelfToolsScopeEnv] = SelfToolsScopeStore
		}
		if cfg.AgentCredential != nil && sessionID != "" {
			token, tokErr := cfg.AgentCredential(sessionID)
			if tokErr != nil {
				return "", "", nil, nil, fmt.Errorf("agent: mint session credential: %w", tokErr)
			}
			env[AuthTokenEnv] = token
		}
	}
	return mcpServerID(cfg), cfg.BinaryPath, args, env, nil
}
