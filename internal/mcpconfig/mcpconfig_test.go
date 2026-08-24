package mcpconfig

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestParse_Valid(t *testing.T) {
	input := `{
		"mcpServers": {
			"my-server": {
				"command": "/usr/bin/server",
				"args": ["--port", "8080"],
				"env": {"API_KEY": "secret"}
			},
			"sse-server": {
				"url": "http://localhost:9000"
			}
		}
	}`

	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.MCPServers))
	}

	s := cfg.MCPServers["my-server"]
	if s.Command != "/usr/bin/server" {
		t.Errorf("command = %q, want /usr/bin/server", s.Command)
	}
	if len(s.Args) != 2 || s.Args[0] != "--port" || s.Args[1] != "8080" {
		t.Errorf("args = %v, want [--port 8080]", s.Args)
	}
	if s.Env["API_KEY"] != "secret" {
		t.Errorf("env[API_KEY] = %q, want secret", s.Env["API_KEY"])
	}

	sse := cfg.MCPServers["sse-server"]
	if sse.URL != "http://localhost:9000" {
		t.Errorf("url = %q, want http://localhost:9000", sse.URL)
	}
}

func TestParse_MissingKey(t *testing.T) {
	_, err := Parse([]byte(`{"other": {}}`))
	if err == nil {
		t.Fatal("expected error for missing mcpServers key")
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestToStoreConfigs(t *testing.T) {
	cfg := &ClaudeCodeConfig{
		MCPServers: map[string]ServerEntry{
			"beta": {
				Command: "/bin/beta",
				Args:    []string{"--verbose"},
				Env:     map[string]string{"FOO": "bar"},
			},
			"alpha": {
				URL: "http://localhost:3000",
			},
		},
	}

	configs := ToStoreConfigs(cfg)
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	// Sorted by name
	if configs[0].Name != "alpha" {
		t.Errorf("first config name = %q, want alpha", configs[0].Name)
	}
	if configs[0].TransportType != "sse" {
		t.Errorf("alpha transport = %q, want sse", configs[0].TransportType)
	}
	if configs[0].URL != "http://localhost:3000" {
		t.Errorf("alpha url = %q", configs[0].URL)
	}

	if configs[1].Name != "beta" {
		t.Errorf("second config name = %q, want beta", configs[1].Name)
	}
	if configs[1].TransportType != "stdio" {
		t.Errorf("beta transport = %q, want stdio", configs[1].TransportType)
	}
	if configs[1].Command != "/bin/beta" {
		t.Errorf("beta command = %q", configs[1].Command)
	}

	var args []string
	json.Unmarshal([]byte(configs[1].Args), &args)
	if len(args) != 1 || args[0] != "--verbose" {
		t.Errorf("beta args = %v, want [--verbose]", args)
	}

	var env []string
	json.Unmarshal([]byte(configs[1].Env), &env)
	if len(env) != 1 || env[0] != "FOO=bar" {
		t.Errorf("beta env = %v, want [FOO=bar]", env)
	}
}

func TestImportAndExport_Roundtrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close(context.Background())

	input := `{
		"mcpServers": {
			"server-a": {
				"command": "/usr/bin/a",
				"args": ["--mode", "test"],
				"env": {"KEY": "val"}
			},
			"server-b": {
				"url": "http://localhost:5000"
			}
		}
	}`

	// Import
	result, err := Import(s, []byte(input))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(result.Created) != 2 {
		t.Errorf("created = %v, want 2 entries", result.Created)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("skipped = %v, want 0 entries", result.Skipped)
	}

	// Import again — should skip both
	result2, err := Import(s, []byte(input))
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(result2.Created) != 0 {
		t.Errorf("second import created = %v, want 0", result2.Created)
	}
	if len(result2.Skipped) != 2 {
		t.Errorf("second import skipped = %v, want 2", result2.Skipped)
	}

	// Export
	cfg, err := Export(s)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("exported %d servers, want 2", len(cfg.MCPServers))
	}

	a := cfg.MCPServers["server-a"]
	if a.Command != "/usr/bin/a" {
		t.Errorf("exported server-a command = %q", a.Command)
	}
	if len(a.Args) != 2 || a.Args[0] != "--mode" {
		t.Errorf("exported server-a args = %v", a.Args)
	}
	if a.Env["KEY"] != "val" {
		t.Errorf("exported server-a env = %v", a.Env)
	}

	b := cfg.MCPServers["server-b"]
	if b.URL != "http://localhost:5000" {
		t.Errorf("exported server-b url = %q", b.URL)
	}

	// Marshal should produce valid JSON
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var check ClaudeCodeConfig
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("re-parse exported JSON: %v", err)
	}
	if len(check.MCPServers) != 2 {
		t.Errorf("re-parsed %d servers, want 2", len(check.MCPServers))
	}
}

func TestImport_EmptyEnvAndArgs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close(context.Background())

	input := `{"mcpServers": {"minimal": {"command": "echo"}}}`

	result, err := Import(s, []byte(input))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(result.Created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(result.Created))
	}

	// Verify DB record has proper defaults
	cfg, err := s.GetMCPServer(context.Background(), "minimal")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg.Args != "[]" {
		t.Errorf("args = %q, want []", cfg.Args)
	}
	if cfg.Env != "[]" {
		t.Errorf("env = %q, want []", cfg.Env)
	}

	// Export should produce clean entry with no empty arrays
	exported, _ := Export(s)
	entry := exported.MCPServers["minimal"]
	if len(entry.Args) != 0 {
		t.Errorf("exported args = %v, want nil/empty", entry.Args)
	}
	if len(entry.Env) != 0 {
		t.Errorf("exported env = %v, want nil/empty", entry.Env)
	}
}

func TestMarshal_Format(t *testing.T) {
	cfg := &ClaudeCodeConfig{
		MCPServers: map[string]ServerEntry{
			"test": {Command: "echo"},
		},
	}
	data, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Should be indented
	if len(data) < 20 {
		t.Errorf("output too short, probably not indented: %s", data)
	}

	// Verify it can be written and read as a file
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	read, _ := os.ReadFile(path)
	var check ClaudeCodeConfig
	if err := json.Unmarshal(read, &check); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
}
