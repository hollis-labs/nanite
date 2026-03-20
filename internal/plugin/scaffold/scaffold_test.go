package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_BasicPlugin(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "test-plugin")

	opts := Options{
		Name:        "test-plugin",
		Description: "A test plugin",
		OutputDir:   outDir,
	}

	if err := Run(opts); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify plugin.yaml exists and has correct content
	yamlContent, err := os.ReadFile(filepath.Join(outDir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("read plugin.yaml: %v", err)
	}
	if !strings.Contains(string(yamlContent), "name: test-plugin") {
		t.Errorf("plugin.yaml missing name, got:\n%s", yamlContent)
	}
	if !strings.Contains(string(yamlContent), "A test plugin") {
		t.Errorf("plugin.yaml missing description, got:\n%s", yamlContent)
	}

	// Verify plugin.go exists and has correct content
	goContent, err := os.ReadFile(filepath.Join(outDir, "plugin.go"))
	if err != nil {
		t.Fatalf("read plugin.go: %v", err)
	}
	if !strings.Contains(string(goContent), "package testplugin") {
		t.Errorf("plugin.go missing package name, got:\n%s", goContent)
	}
	if !strings.Contains(string(goContent), `ID() string          { return "test-plugin" }`) {
		t.Errorf("plugin.go missing ID method, got:\n%s", goContent)
	}
	if !strings.Contains(string(goContent), "TestPluginPlugin") {
		t.Errorf("plugin.go missing struct name, got:\n%s", goContent)
	}

	// Verify README.md exists
	if _, err := os.Stat(filepath.Join(outDir, "README.md")); err != nil {
		t.Errorf("README.md not created: %v", err)
	}

	// Verify no agents dir created
	if _, err := os.Stat(filepath.Join(outDir, "agents")); !os.IsNotExist(err) {
		t.Error("agents directory should not exist without --with-agent")
	}
}

func TestRun_WithAgent(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "my-agent")

	opts := Options{
		Name:      "my-agent",
		WithAgent: true,
		OutputDir: outDir,
	}

	if err := Run(opts); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	agentPath := filepath.Join(outDir, "agents", "my-agent.yaml")
	content, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent.yaml: %v", err)
	}
	if !strings.Contains(string(content), "slug: my-agent") {
		t.Errorf("agent.yaml missing slug, got:\n%s", content)
	}
}

func TestRun_WithEnvelope(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "my-plugin")

	opts := Options{
		Name:      "my-plugin",
		Envelopes: []EnvelopeDef{ToEnvelopeDef("card")},
		OutputDir: outDir,
	}

	if err := Run(opts); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	envPath := filepath.Join(outDir, "ui", "CardCard.tsx")
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read CardCard.tsx: %v", err)
	}
	if !strings.Contains(string(content), "export function CardCard") {
		t.Errorf("envelope missing export, got:\n%s", content)
	}

	// Check plugin.yaml registers the envelope
	yamlContent, err := os.ReadFile(filepath.Join(outDir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("read plugin.yaml: %v", err)
	}
	if !strings.Contains(string(yamlContent), "type: card") {
		t.Errorf("plugin.yaml missing envelope registration, got:\n%s", yamlContent)
	}
}

func TestRun_WithCRUD(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "my-plugin")

	opts := Options{
		Name:          "my-plugin",
		CRUDResources: []string{"items"},
		OutputDir:     outDir,
	}

	if err := Run(opts); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	goContent, err := os.ReadFile(filepath.Join(outDir, "plugin.go"))
	if err != nil {
		t.Fatalf("read plugin.go: %v", err)
	}
	if !strings.Contains(string(goContent), "ItemsHandler") {
		t.Errorf("plugin.go missing CRUD handler, got:\n%s", goContent)
	}
	if !strings.Contains(string(goContent), `RegisterCRUDHandler("items"`) {
		t.Errorf("plugin.go missing CRUD registration, got:\n%s", goContent)
	}
}

func TestRun_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "existing")
	os.MkdirAll(outDir, 0755)
	os.WriteFile(filepath.Join(outDir, "plugin.yaml"), []byte("name: existing"), 0644)

	opts := Options{
		Name:      "existing",
		OutputDir: outDir,
	}

	err := Run(opts)
	if err == nil {
		t.Fatal("expected error for existing plugin")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToEnvelopeDef(t *testing.T) {
	def := ToEnvelopeDef("card")
	if def.Type != "card" {
		t.Errorf("Type = %q, want %q", def.Type, "card")
	}
	if def.Export != "CardCard" {
		t.Errorf("Export = %q, want %q", def.Export, "CardCard")
	}
}

func TestToStructName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"my-plugin", "MyPluginPlugin"},
		{"simple", "SimplePlugin"},
		{"multi-word-name", "MultiWordNamePlugin"},
	}
	for _, tt := range tests {
		got := toStructName(tt.in)
		if got != tt.want {
			t.Errorf("toStructName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToPackageName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"my-plugin", "myplugin"},
		{"simple", "simple"},
		{"multi-word-name", "multiwordname"},
	}
	for _, tt := range tests {
		got := toPackageName(tt.in)
		if got != tt.want {
			t.Errorf("toPackageName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
