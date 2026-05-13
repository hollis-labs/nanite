package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMD_ValidFile(t *testing.T) {
	data := []byte(`---
name: Test Agent
slug: test-agent
description: A test agent
model: claude-sonnet-4-20250514
tools:
  - read
  - write
skills:
  - go-build
tags:
  - backend
  - go
---
You are a test agent. Help the user with testing.
`)

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if def.Name != "Test Agent" {
		t.Errorf("Name = %q, want %q", def.Name, "Test Agent")
	}
	if def.Slug != "test-agent" {
		t.Errorf("Slug = %q, want %q", def.Slug, "test-agent")
	}
	if def.Description != "A test agent" {
		t.Errorf("Description = %q, want %q", def.Description, "A test agent")
	}
	if def.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q, want %q", def.Model, "claude-sonnet-4-20250514")
	}
	if len(def.Tools) != 2 || def.Tools[0] != "read" || def.Tools[1] != "write" {
		t.Errorf("Tools = %v, want [read write]", def.Tools)
	}
	if len(def.Skills) != 1 || def.Skills[0] != "go-build" {
		t.Errorf("Skills = %v, want [go-build]", def.Skills)
	}
	if len(def.Tags) != 2 {
		t.Errorf("Tags = %v, want [backend go]", def.Tags)
	}
	if def.SystemPrompt != "You are a test agent. Help the user with testing." {
		t.Errorf("SystemPrompt = %q, want %q", def.SystemPrompt, "You are a test agent. Help the user with testing.")
	}
}

func TestParseMD_AllFields(t *testing.T) {
	// CW-20260512-0123 (SP-20260512-0011 W3): the `constraints:` block
	// is intentionally retained in this fixture (with the legacy keys
	// `maxIterations` / `maxTimeSeconds` / `retryBudget`) to exercise
	// the tolerant-parse path — yaml.Unmarshal ignores unknown keys
	// against the empty AgentConstraints struct, so old frontmatter
	// continues to load without error even though the values are no
	// longer honored. Validation warns on these keys at the API layer
	// (see internal/agentvalidation/validation.go).
	data := []byte(`---
name: Full Agent
slug: full-agent
description: All fields populated
icon: code
avatar: https://example.com/avatar.png
tags: [a, b]
model: gpt-4
tools: [read, write, bash]
permissionMode: yolo
maxTurns: 25
skills: [go-build, go-test]
mcpServers: [engine, conduit]
memory: session
effort: high
isolation: worktree
directories: [./src, ./tests]
constraints:
  maxIterations: 50
  maxTimeSeconds: 300
  retryBudget: 3
modes:
  - slug: default
    name: Default
    promptAddendum: Be helpful.
  - slug: architect
    name: Architect
    promptAddendum: Focus on design.
    toolOverrides:
      prefer: [read, grep]
---
System prompt body here.
`)

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if def.Icon != "code" {
		t.Errorf("Icon = %q, want %q", def.Icon, "code")
	}
	if def.Avatar != "https://example.com/avatar.png" {
		t.Errorf("Avatar = %q", def.Avatar)
	}
	if def.PermissionMode != "yolo" {
		t.Errorf("PermissionMode = %q", def.PermissionMode)
	}
	if def.MaxTurns != 25 {
		t.Errorf("MaxTurns = %d", def.MaxTurns)
	}
	if len(def.MCPServers) != 2 {
		t.Errorf("MCPServers = %v", def.MCPServers)
	}
	if def.Memory != "session" {
		t.Errorf("Memory = %q", def.Memory)
	}
	if def.Effort != "high" {
		t.Errorf("Effort = %q", def.Effort)
	}
	if def.Isolation != "worktree" {
		t.Errorf("Isolation = %q", def.Isolation)
	}
	if len(def.Directories) != 2 {
		t.Errorf("Directories = %v", def.Directories)
	}
	// AgentConstraints is now an empty struct (CW-20260512-0123); just
	// verify parse tolerates the legacy keys without failing. The
	// surviving turn-count knob (`maxTurns`) is asserted above.
	if def.Constraints != (AgentConstraints{}) {
		t.Errorf("Constraints = %+v, want empty struct (CW-20260512-0123)", def.Constraints)
	}
	if len(def.Modes) != 2 {
		t.Fatalf("Modes len = %d, want 2", len(def.Modes))
	}
	if def.Modes[0].Slug != "default" {
		t.Errorf("Modes[0].Slug = %q", def.Modes[0].Slug)
	}
	if def.Modes[1].PromptAddendum != "Focus on design." {
		t.Errorf("Modes[1].PromptAddendum = %q", def.Modes[1].PromptAddendum)
	}
	if def.Modes[1].ToolOverrides == nil {
		t.Error("Modes[1].ToolOverrides is nil")
	}
}

func TestParseMD_EmptyBody(t *testing.T) {
	data := []byte(`---
name: Minimal
slug: minimal
---
`)

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.SystemPrompt != "" {
		t.Errorf("SystemPrompt = %q, want empty", def.SystemPrompt)
	}
}

func TestParseMD_NoBody(t *testing.T) {
	data := []byte("---\nname: Minimal\nslug: minimal\n---")

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.SystemPrompt != "" {
		t.Errorf("SystemPrompt = %q, want empty", def.SystemPrompt)
	}
}

func TestParseMD_MissingFrontmatter(t *testing.T) {
	data := []byte("Just a markdown file with no frontmatter.")
	_, err := ParseMD(data)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseMD_MalformedYAML(t *testing.T) {
	data := []byte("---\n: invalid: yaml: [[\n---\nbody")
	_, err := ParseMD(data)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestParseMD_MissingSlug(t *testing.T) {
	data := []byte("---\nname: No Slug\n---\nbody")
	_, err := ParseMD(data)
	if err == nil {
		t.Fatal("expected error for missing slug")
	}
}

func TestParseMD_MissingClosingDelimiter(t *testing.T) {
	data := []byte("---\nname: Broken\nslug: broken\n")
	_, err := ParseMD(data)
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
}

func TestParseMD_LeadingNewlines(t *testing.T) {
	data := []byte("\n\n---\nname: Padded\nslug: padded\n---\nbody")
	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Slug != "padded" {
		t.Errorf("Slug = %q, want %q", def.Slug, "padded")
	}
}

func TestParseMDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-agent.md")
	content := []byte("---\nname: File Agent\nslug: file-agent\n---\nHello from file.")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	def, err := ParseMDFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Slug != "file-agent" {
		t.Errorf("Slug = %q", def.Slug)
	}
	if def.SourceRef != path {
		t.Errorf("SourceRef = %q, want %q", def.SourceRef, path)
	}
	if def.SystemPrompt != "Hello from file." {
		t.Errorf("SystemPrompt = %q", def.SystemPrompt)
	}
}

func TestParseMDFile_NotFound(t *testing.T) {
	_, err := ParseMDFile("/nonexistent/path.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSlugFromFilename(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"code-agent.md", "code-agent"},
		{"/path/to/research.md", "research"},
		{"simple.md", "simple"},
		{"no-ext", "no-ext"},
	}
	for _, tt := range tests {
		got := SlugFromFilename(tt.path)
		if got != tt.want {
			t.Errorf("SlugFromFilename(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestParseMD_MultilineSystemPrompt(t *testing.T) {
	data := []byte(`---
name: Multi
slug: multi
---
First paragraph.

Second paragraph with **markdown**.

- List item 1
- List item 2
`)

	def, err := ParseMD(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(def.SystemPrompt, "First paragraph.") ||
		!contains(def.SystemPrompt, "Second paragraph") ||
		!contains(def.SystemPrompt, "- List item 2") {
		t.Errorf("SystemPrompt missing expected content: %q", def.SystemPrompt)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
