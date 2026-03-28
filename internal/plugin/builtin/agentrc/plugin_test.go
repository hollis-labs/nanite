package agentrc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComposeSystemPrompt(t *testing.T) {
	// Create temp role files.
	dir := t.TempDir()
	domainDir := filepath.Join(dir, "domain")
	stackDir := filepath.Join(dir, "stack")
	os.MkdirAll(domainDir, 0o755)
	os.MkdirAll(stackDir, 0o755)

	os.WriteFile(filepath.Join(domainDir, "backend.md"), []byte("# Role: Backend\nYou are a backend engineer."), 0o644)
	os.WriteFile(filepath.Join(stackDir, "go.md"), []byte("# Role: Go\nFollow Go conventions."), 0o644)

	globalCfg := &globalConfig{
		Roles: map[string]roleEntry{
			"backend": {File: "domain/backend.md", Type: "domain"},
			"go":      {File: "stack/go.md", Type: "stack"},
		},
	}

	result := composeSystemPrompt(globalCfg, dir, []string{"backend", "go"})

	if result == "" {
		t.Fatal("expected non-empty system prompt")
	}
	if got := result; got == "" {
		t.Fatal("empty result")
	}
	if !contains(result, "Backend") {
		t.Errorf("expected prompt to contain 'Backend', got: %s", result)
	}
	if !contains(result, "Go conventions") {
		t.Errorf("expected prompt to contain 'Go conventions', got: %s", result)
	}
}

func TestComposeSystemPrompt_MissingRole(t *testing.T) {
	globalCfg := &globalConfig{Roles: map[string]roleEntry{}}
	result := composeSystemPrompt(globalCfg, t.TempDir(), []string{"missing"})
	if !contains(result, "not found") {
		t.Errorf("expected 'not found' for missing role, got: %s", result)
	}
}

func TestReadProjectConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(`
agentrc_version: "2.2.0"
agents:
  test-agent:
    name: Test Agent
    description: A test agent
    roles: [backend]
    skills: [go-build]
    context: agents/test.md
`), 0o644)

	cfg, err := readProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("readProjectConfig: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
	a := cfg.Agents["test-agent"]
	if a.Name != "Test Agent" {
		t.Errorf("expected name 'Test Agent', got %q", a.Name)
	}
	if len(a.Roles) != 1 || a.Roles[0] != "backend" {
		t.Errorf("unexpected roles: %v", a.Roles)
	}
}

func TestReadProjectConfig_Missing(t *testing.T) {
	_, err := readProjectConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
