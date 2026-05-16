package agent

import (
	"encoding/json"
	"testing"
)

// decodeMCPServer pulls the single server entry out of a rendered .mcp.json.
func decodeMCPServer(t *testing.T, body, serverID string) map[string]any {
	t.Helper()
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("unmarshal .mcp.json: %v\nbody=%s", err, body)
	}
	srv, ok := doc.MCPServers[serverID]
	if !ok {
		t.Fatalf("server %q missing from .mcp.json: %s", serverID, body)
	}
	return srv
}

// TestRenderMCPJSON_APIBaseURLPlantsEnv pins the CLI-launch self-tools
// proxy contract: when MCPConfig.APIBaseURL is set, the planted .mcp.json
// must carry it as the NANITE_API_URL env var so the spawned `nanite mcp`
// subprocess forwards self-tool calls to the live harness. An empty
// APIBaseURL must leave the env block empty (local-dispatch mode).
func TestRenderMCPJSON_APIBaseURLPlantsEnv(t *testing.T) {
	t.Run("APIBaseURL set — env carries NANITE_API_URL", func(t *testing.T) {
		body, err := renderMCPJSON(MCPConfig{
			BinaryPath: "/usr/local/bin/nanite",
			DBPath:     "/data/nanite.db",
			ServerID:   "nanite",
			APIBaseURL: "http://127.0.0.1:8090",
		}, "sess-1")
		if err != nil {
			t.Fatalf("renderMCPJSON: %v", err)
		}
		srv := decodeMCPServer(t, body, "nanite")
		env, ok := srv["env"].(map[string]any)
		if !ok {
			t.Fatalf("server env is not an object: %#v", srv["env"])
		}
		if got := env["NANITE_API_URL"]; got != "http://127.0.0.1:8090" {
			t.Errorf("NANITE_API_URL = %v, want http://127.0.0.1:8090", got)
		}
	})

	t.Run("APIBaseURL empty — env block is empty", func(t *testing.T) {
		body, err := renderMCPJSON(MCPConfig{
			BinaryPath: "/usr/local/bin/nanite",
			DBPath:     "/data/nanite.db",
			ServerID:   "nanite",
		}, "sess-1")
		if err != nil {
			t.Fatalf("renderMCPJSON: %v", err)
		}
		srv := decodeMCPServer(t, body, "nanite")
		env, ok := srv["env"].(map[string]any)
		if !ok {
			t.Fatalf("server env is not an object: %#v", srv["env"])
		}
		if len(env) != 0 {
			t.Errorf("env should be empty when APIBaseURL is unset, got %#v", env)
		}
	})
}
