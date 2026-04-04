package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// yamlToolDef is the on-disk format for a YAML-defined tool.
type yamlToolDef struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Category    string         `yaml:"category"`
	InputSchema map[string]any `yaml:"inputSchema"`
	Execute     yamlExecute    `yaml:"execute"`
	IsReadOnly  bool           `yaml:"isReadOnly"`
	Tags        []string       `yaml:"tags"`
	Timeout     string         `yaml:"timeout"` // duration string, e.g. "30s"
}

// yamlExecute describes how a YAML tool runs.
type yamlExecute struct {
	Type      string         `yaml:"type"`      // "shell" or "hadron"
	Command   string         `yaml:"command"`   // shell: template with {{.field}}
	Blueprint string         `yaml:"blueprint"` // hadron: blueprint name
	Inputs    map[string]any `yaml:"inputs"`    // hadron: input mappings
	Timeout   string         `yaml:"timeout"`   // per-execution timeout override
}

// LoadYAMLTools discovers and loads YAML tool definitions from the standard
// locations: .nanite/tools/*.yaml (project) and ~/.nanite/tools/*.yaml (user).
// workingDir is the project root; if empty, the current directory is used.
func LoadYAMLTools(workingDir string) []Tool {
	if workingDir == "" {
		workingDir = "."
	}

	var tools []Tool

	// Project-level tools.
	projectDir := filepath.Join(workingDir, ".nanite", "tools")
	tools = append(tools, loadYAMLDir(projectDir)...)

	// User-level tools.
	home, err := os.UserHomeDir()
	if err == nil {
		userDir := filepath.Join(home, ".nanite", "tools")
		tools = append(tools, loadYAMLDir(userDir)...)
	}

	if len(tools) > 0 {
		log.Printf("tool/yaml: loaded %d YAML tools", len(tools))
	}

	return tools
}

// loadYAMLDir reads all *.yaml files from a directory and returns tools.
func loadYAMLDir(dir string) []Tool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // directory doesn't exist — not an error
	}

	var tools []Tool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		t, err := loadYAMLFile(path)
		if err != nil {
			log.Printf("tool/yaml: skipping %s: %v", path, err)
			continue
		}
		tools = append(tools, t)
	}
	return tools
}

// loadYAMLFile parses a single YAML tool definition file into a Tool.
func loadYAMLFile(path string) (Tool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var def yamlToolDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if def.Name == "" {
		return nil, fmt.Errorf("%s: name is required", path)
	}
	if def.Execute.Type == "" {
		return nil, fmt.Errorf("%s: execute.type is required", path)
	}
	if def.Execute.Type != "shell" && def.Execute.Type != "hadron" {
		return nil, fmt.Errorf("%s: execute.type must be 'shell' or 'hadron', got %q", path, def.Execute.Type)
	}

	var timeout time.Duration
	if def.Timeout != "" {
		timeout, err = time.ParseDuration(def.Timeout)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid timeout %q: %w", path, def.Timeout, err)
		}
	}

	var execTimeout time.Duration
	if def.Execute.Timeout != "" {
		execTimeout, err = time.ParseDuration(def.Execute.Timeout)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid execute.timeout %q: %w", path, def.Execute.Timeout, err)
		}
	}
	if execTimeout > 0 && timeout == 0 {
		timeout = execTimeout
	}

	opts := []ToolOption{
		WithCategory(def.Category),
		WithSource(SourceYAML),
		WithTags(def.Tags...),
		WithTimeout(timeout),
		WithReadOnly(def.IsReadOnly),
	}

	if len(def.InputSchema) > 0 {
		opts = append(opts, WithSchemaMap(def.InputSchema))
	}

	// If read-only, also mark as concurrency-safe (safe assumption for YAML tools).
	if def.IsReadOnly {
		opts = append(opts, WithConcurrencySafe(true))
	}

	switch def.Execute.Type {
	case "shell":
		opts = append(opts, WithCallFunc(makeShellCallFunc(def.Execute.Command, execTimeout)))
	case "hadron":
		opts = append(opts, WithCallFunc(makeHadronCallFunc(def.Execute.Blueprint, def.Execute.Inputs, execTimeout)))
	}

	return NewTool(def.Name, def.Description, opts...), nil
}

// makeShellCallFunc creates a CallFunc that executes a shell command template.
func makeShellCallFunc(cmdTemplate string, timeout time.Duration) CallFunc {
	return func(ctx context.Context, input map[string]any, execCtx ExecutionContext) (*ToolResult, error) {
		// Render the command template with input values.
		tmpl, err := template.New("cmd").Parse(cmdTemplate)
		if err != nil {
			return &ToolResult{Output: fmt.Sprintf("Error: invalid command template: %v", err), IsError: true}, nil
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, input); err != nil {
			return &ToolResult{Output: fmt.Sprintf("Error: template render failed: %v", err), IsError: true}, nil
		}

		rendered := buf.String()

		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		cmd := exec.CommandContext(ctx, "sh", "-c", rendered)
		if execCtx.WorkingDir != "" {
			cmd.Dir = execCtx.WorkingDir
		}

		out, err := cmd.CombinedOutput()
		if err != nil {
			return &ToolResult{
				Output:  fmt.Sprintf("%s\nError: %v", string(out), err),
				IsError: true,
			}, nil
		}

		return &ToolResult{Output: string(out)}, nil
	}
}

// makeHadronCallFunc creates a CallFunc that invokes a Hadron blueprint.
func makeHadronCallFunc(blueprint string, inputMappings map[string]any, timeout time.Duration) CallFunc {
	return func(ctx context.Context, input map[string]any, _ ExecutionContext) (*ToolResult, error) {
		// Build the Hadron CLI arguments.
		args := []string{"run", blueprint}

		// Resolve input mappings — values may be Go templates referencing input.
		for key, valTmpl := range inputMappings {
			valStr, ok := valTmpl.(string)
			if !ok {
				args = append(args, "--input", fmt.Sprintf("%s=%v", key, valTmpl))
				continue
			}

			// Render template references like {{.path}}.
			tmpl, err := template.New(key).Parse(valStr)
			if err != nil {
				args = append(args, "--input", fmt.Sprintf("%s=%s", key, valStr))
				continue
			}
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, input); err != nil {
				args = append(args, "--input", fmt.Sprintf("%s=%s", key, valStr))
				continue
			}
			args = append(args, "--input", fmt.Sprintf("%s=%s", key, buf.String()))
		}

		args = append(args, "--output", "json")

		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		cmd := exec.CommandContext(ctx, "hadron", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &ToolResult{
				Output:  fmt.Sprintf("%s\nError: %v", string(out), err),
				IsError: true,
			}, nil
		}

		return &ToolResult{Output: string(out)}, nil
	}
}

// ParseYAMLToolBytes parses a single YAML tool definition from raw bytes.
// Useful for testing without touching the filesystem.
func ParseYAMLToolBytes(data []byte) (Tool, error) {
	var def yamlToolDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if def.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// For testing/embedding, allow tools without an execute block.
	var timeout time.Duration
	if def.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(def.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q: %w", def.Timeout, err)
		}
	}

	opts := []ToolOption{
		WithCategory(def.Category),
		WithSource(SourceYAML),
		WithTags(def.Tags...),
		WithTimeout(timeout),
		WithReadOnly(def.IsReadOnly),
	}
	if len(def.InputSchema) > 0 {
		opts = append(opts, WithSchemaMap(def.InputSchema))
	}
	if def.IsReadOnly {
		opts = append(opts, WithConcurrencySafe(true))
	}

	var execTimeout time.Duration
	if def.Execute.Timeout != "" {
		var err error
		execTimeout, err = time.ParseDuration(def.Execute.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid execute.timeout %q: %w", def.Execute.Timeout, err)
		}
	}

	switch def.Execute.Type {
	case "shell":
		opts = append(opts, WithCallFunc(makeShellCallFunc(def.Execute.Command, execTimeout)))
	case "hadron":
		opts = append(opts, WithCallFunc(makeHadronCallFunc(def.Execute.Blueprint, def.Execute.Inputs, execTimeout)))
	case "":
		// No execute block — tool will error on Call (useful for definition-only).
	default:
		return nil, fmt.Errorf("execute.type must be 'shell' or 'hadron', got %q", def.Execute.Type)
	}

	// Marshal inputSchema for JSON output if present.
	if len(def.InputSchema) > 0 {
		if data, err := json.Marshal(def.InputSchema); err == nil {
			opts = append(opts, WithSchema(json.RawMessage(data)))
		}
	}

	return NewTool(def.Name, def.Description, opts...), nil
}
