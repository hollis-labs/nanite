package nanitenative

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestComposeSystemPrompt(t *testing.T) {
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
nanite_version: "2.3.0"
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

func TestProjectConfigRequiresNanite(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := readProjectConfigFromRoot(dir); err == nil {
		t.Fatal("expected error when .nanite/config.yaml is missing")
	}
}

func TestProjectConfigFromRoot(t *testing.T) {
	dir := t.TempDir()
	naniteDir := filepath.Join(dir, ".nanite")
	os.MkdirAll(naniteDir, 0o755)

	os.WriteFile(filepath.Join(naniteDir, "config.yaml"), []byte(`
agents:
  nanite-agent:
    name: Nanite Agent
`), 0o644)

	cfg, configDir, err := readProjectConfigFromRoot(dir)
	if err != nil {
		t.Fatalf("readProjectConfigFromRoot: %v", err)
	}
	if configDir != naniteDir {
		t.Errorf("expected .nanite/ dir, got %q", configDir)
	}
	if _, ok := cfg.Agents["nanite-agent"]; !ok {
		t.Error("expected nanite-agent in config")
	}
}

func TestAdapterName(t *testing.T) {
	p := New()
	if got := p.Adapter().Name(); got != "nanite-native" {
		t.Errorf("Adapter().Name() = %q, want %q", got, "nanite-native")
	}
}

func TestAdapterPriority(t *testing.T) {
	p := New()
	if got := p.Adapter().Priority(); got != 50 {
		t.Errorf("Adapter().Priority() = %d, want 50", got)
	}
}

func TestDiscoverNoConfig(t *testing.T) {
	p := New()
	defs, err := p.Adapter().Discover(t.TempDir())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("expected 0 definitions for empty dir, got %d", len(defs))
	}
}

func TestPopulateSandbox(t *testing.T) {
	p := New()
	sandbox := t.TempDir()

	ap := store.AgentProfile{
		Slug: "test",
		Name: "Test Agent",
	}
	err := p.Adapter().PopulateSandbox(sandbox, ap, agent.SandboxContext{})
	if err != nil {
		t.Fatalf("PopulateSandbox: %v", err)
	}

	cfgPath := filepath.Join(sandbox, ".nanite", "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", cfgPath)
	}
}

func TestSyncProjectRootNoop(t *testing.T) {
	p := New()
	if err := p.Adapter().SyncProjectRoot(t.TempDir(), nil); err != nil {
		t.Fatalf("SyncProjectRoot: %v", err)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
