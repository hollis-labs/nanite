package tool

import (
	"encoding/json"
	"strings"

	"github.com/hollis-labs/nanite/internal/provider"
)

// WrapProviderDef wraps a provider.ToolDefinition as a Tool interface
// implementation. The resulting tool has no Call function (will error if
// called) — it exists for registration in the broker registry so that
// selection works. Execution still flows through the existing MCP/ToolClient path.
func WrapProviderDef(def provider.ToolDefinition, category, source string, tags []string) Tool {
	opts := []ToolOption{
		WithCategory(category),
		WithSource(source),
		WithSchemaMap(def.InputSchema),
	}
	if len(tags) > 0 {
		opts = append(opts, WithTags(tags...))
	}

	// Infer safety metadata from the tool name.
	opts = append(opts, inferSafetyOptions(def.Name)...)

	return NewTool(def.Name, def.Description, opts...)
}

// ToProviderDefinition converts a Tool back to provider.ToolDefinition
// for the existing chat loop / provider interface.
func ToProviderDefinition(t Tool) provider.ToolDefinition {
	var schema map[string]any
	if raw := t.InputSchema(); raw != nil {
		_ = json.Unmarshal(raw, &schema)
	}
	return provider.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: schema,
	}
}

// ToProviderDefinitions converts a slice of Tools to provider.ToolDefinition.
func ToProviderDefinitions(tools []Tool) []provider.ToolDefinition {
	defs := make([]provider.ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = ToProviderDefinition(t)
	}
	return defs
}

// inferSafetyOptions returns ToolOptions based on well-known tool names.
func inferSafetyOptions(name string) []ToolOption {
	switch name {
	case "dev_read", "dev_grep", "dev_glob":
		return []ToolOption{WithReadOnly(true), WithConcurrencySafe(true)}
	case "dev_write", "dev_edit":
		return []ToolOption{WithReadOnly(false), WithConcurrencySafe(false)}
	case "dev_bash":
		return []ToolOption{
			WithReadOnlyFunc(func(input map[string]any) bool {
				cmd, _ := input["command"].(string)
				return isReadOnlyCommand(cmd)
			}),
			WithDestructiveFunc(func(input map[string]any) bool {
				cmd, _ := input["command"].(string)
				return isDestructiveCommand(cmd)
			}),
			WithConcurrencySafeFunc(func(input map[string]any) bool {
				cmd, _ := input["command"].(string)
				return isReadOnlyCommand(cmd)
			}),
		}
	case "web_fetch", "web_search":
		return []ToolOption{WithReadOnly(true), WithConcurrencySafe(true)}
	case "json_parse", "datetime", "base64_encode", "base64_decode",
		"url_encode", "url_decode", "hash", "math_eval":
		return []ToolOption{WithReadOnly(true), WithConcurrencySafe(true)}
	default:
		return nil
	}
}

// isReadOnlyCommand checks if a shell command is likely read-only.
func isReadOnlyCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	readOnlyPrefixes := []string{
		"ls ", "cat ", "head ", "tail ", "grep ", "find ", "wc ",
		"git log", "git status", "git diff", "git show", "git branch",
		"go vet", "go test", "echo ",
	}
	// Bare commands (no arguments) that are always read-only.
	readOnlyExact := []string{
		"ls", "cat", "head", "tail", "grep", "find", "wc",
		"pwd", "whoami", "date", "env", "echo",
	}
	cmd = strings.TrimLeft(cmd, " \t")
	for _, exact := range readOnlyExact {
		if cmd == exact {
			return true
		}
	}
	for _, prefix := range readOnlyPrefixes {
		if len(cmd) >= len(prefix) && cmd[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// isDestructiveCommand checks if a shell command could cause data loss.
func isDestructiveCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	destructivePrefixes := []string{
		"rm ", "rm\t", "rmdir ", "git reset", "git clean",
		"git push --force", "git push -f", "drop ", "truncate ",
	}
	for _, prefix := range destructivePrefixes {
		if len(cmd) >= len(prefix) && cmd[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
