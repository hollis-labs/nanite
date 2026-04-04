package tool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/tool"
)

const testYAMLShell = `
name: my-lint
description: Run custom linting rules
category: code
inputSchema:
  type: object
  properties:
    path:
      type: string
      description: File or directory to lint
  required: [path]
execute:
  type: shell
  command: "echo linting {{.path}}"
  timeout: 30s
isReadOnly: true
tags: [code, lint, quality]
timeout: 45s
`

const testYAMLHadron = `
name: run-pipeline
description: Run a Hadron pipeline
category: agent
inputSchema:
  type: object
  properties:
    target:
      type: string
execute:
  type: hadron
  blueprint: my-blueprint
  inputs:
    target: "{{.target}}"
tags: [hadron, automation]
`

const testYAMLMinimal = `
name: minimal-tool
description: A minimal tool
execute:
  type: shell
  command: "echo hello"
`

const testYAMLInvalid_NoName = `
description: missing name field
execute:
  type: shell
  command: "echo oops"
`

const testYAMLInvalid_BadType = `
name: bad-type
description: unsupported execute type
execute:
  type: python
  command: "print('hi')"
`

func TestParseYAMLToolBytes_Shell(t *testing.T) {
	tt, err := tool.ParseYAMLToolBytes([]byte(testYAMLShell))
	if err != nil {
		t.Fatalf("ParseYAMLToolBytes() error: %v", err)
	}

	if tt.Name() != "my-lint" {
		t.Errorf("Name() = %q", tt.Name())
	}
	if tt.Description() != "Run custom linting rules" {
		t.Errorf("Description() = %q", tt.Description())
	}
	if tt.Category() != "code" {
		t.Errorf("Category() = %q", tt.Category())
	}
	if tt.Source() != tool.SourceYAML {
		t.Errorf("Source() = %q", tt.Source())
	}
	if tt.Timeout() != 45*time.Second {
		t.Errorf("Timeout() = %v, want 45s", tt.Timeout())
	}

	tags := tt.Tags()
	if len(tags) != 3 || tags[0] != "code" || tags[1] != "lint" || tags[2] != "quality" {
		t.Errorf("Tags() = %v", tags)
	}

	if !tt.IsReadOnly(nil) {
		t.Error("IsReadOnly() = false, want true")
	}
	if !tt.IsConcurrencySafe(nil) {
		t.Error("IsConcurrencySafe() = false, want true (read-only implies safe)")
	}

	// Schema should be valid JSON.
	var schema map[string]any
	if err := json.Unmarshal(tt.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema() invalid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v", schema["type"])
	}

	// Call should execute the shell command.
	result, err := tt.Call(context.Background(), map[string]any{"path": "/tmp/test"}, tool.ExecutionContext{})
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if result.IsError {
		t.Errorf("Call() isError=true, output: %s", result.Output)
	}
	if result.Output != "linting /tmp/test\n" {
		t.Errorf("Call() output = %q, want %q", result.Output, "linting /tmp/test\n")
	}
}

func TestParseYAMLToolBytes_Hadron(t *testing.T) {
	tt, err := tool.ParseYAMLToolBytes([]byte(testYAMLHadron))
	if err != nil {
		t.Fatalf("ParseYAMLToolBytes() error: %v", err)
	}

	if tt.Name() != "run-pipeline" {
		t.Errorf("Name() = %q", tt.Name())
	}
	if tt.Category() != "agent" {
		t.Errorf("Category() = %q", tt.Category())
	}

	tags := tt.Tags()
	if len(tags) != 2 || tags[0] != "hadron" {
		t.Errorf("Tags() = %v", tags)
	}
}

func TestParseYAMLToolBytes_Minimal(t *testing.T) {
	tt, err := tool.ParseYAMLToolBytes([]byte(testYAMLMinimal))
	if err != nil {
		t.Fatalf("ParseYAMLToolBytes() error: %v", err)
	}

	if tt.Name() != "minimal-tool" {
		t.Errorf("Name() = %q", tt.Name())
	}
	if tt.Category() != "" {
		t.Errorf("Category() = %q, want empty", tt.Category())
	}
	if len(tt.Tags()) != 0 {
		t.Errorf("Tags() = %v, want empty", tt.Tags())
	}
}

func TestParseYAMLToolBytes_Errors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"no name", testYAMLInvalid_NoName},
		{"bad type", testYAMLInvalid_BadType},
		{"invalid yaml", "{{{{not yaml"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.ParseYAMLToolBytes([]byte(tc.yaml))
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadYAMLTools_Directory(t *testing.T) {
	// Create a temp directory with tool YAML files.
	tmpDir := t.TempDir()
	toolsDir := filepath.Join(tmpDir, ".nanite", "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write two valid tool files and one invalid.
	if err := os.WriteFile(filepath.Join(toolsDir, "lint.yaml"), []byte(testYAMLShell), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "minimal.yaml"), []byte(testYAMLMinimal), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "bad.yaml"), []byte(testYAMLInvalid_NoName), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-yaml file should be ignored.
	if err := os.WriteFile(filepath.Join(toolsDir, "readme.txt"), []byte("not a tool"), 0o644); err != nil {
		t.Fatal(err)
	}

	tools := tool.LoadYAMLTools(tmpDir)
	// Should load 2 valid tools (bad.yaml skipped, readme.txt ignored).
	if len(tools) != 2 {
		t.Errorf("LoadYAMLTools() returned %d tools, want 2", len(tools))
	}
}

func TestLoadYAMLTools_NoDirectory(t *testing.T) {
	// Non-existent directory should return empty, not error.
	tools := tool.LoadYAMLTools("/nonexistent/path/that/does/not/exist")
	if len(tools) != 0 {
		t.Errorf("LoadYAMLTools() returned %d tools for nonexistent dir", len(tools))
	}
}
