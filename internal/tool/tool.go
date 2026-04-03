// Package tool defines the canonical tool interface for Conduit.
//
// All tools — builtin, MCP-bridged, YAML-defined, plugin-provided —
// implement the Tool interface. The builder pattern (NewTool + ToolOption)
// is the primary way to construct tools in Go code. YAML-defined tools
// are loaded separately via the yaml_loader.
package tool

import (
	"context"
	"encoding/json"
	"time"
)

// Tool categories.
const (
	CategoryCoreIO  = "core-io"
	CategorySearch  = "search"
	CategoryMCP     = "mcp"
	CategoryAgent   = "agent"
	CategorySession = "session"
	CategoryContext = "context"
	CategoryMode    = "mode"
)

// Tool sources — where a tool was registered from.
const (
	SourceBuiltin = "builtin"
	SourceMCP     = "mcp"
	SourcePlugin  = "plugin"
	SourceUser    = "user"
	SourceYAML    = "yaml"
)

// Tool is the canonical tool interface. Every tool in the system implements
// this regardless of origin (builtin Go code, MCP server, YAML file, plugin).
type Tool interface {
	Name() string
	Description() string
	Category() string
	InputSchema() json.RawMessage
	Source() string
	Tags() []string
	Timeout() time.Duration

	// Call executes the tool. Input is the raw LLM-provided map (JSON wire
	// format); each implementation parses it into typed fields internally.
	Call(ctx context.Context, input map[string]any, execCtx ExecutionContext) (*ToolResult, error)

	// ValidateInput checks the input map against the tool's schema/constraints
	// before execution. Returns nil if valid.
	ValidateInput(input map[string]any) error

	// Input-dependent metadata. These take the input because the answer can
	// vary per invocation (e.g., shell "ls" is safe, shell "rm -rf" is not).
	IsConcurrencySafe(input map[string]any) bool
	IsReadOnly(input map[string]any) bool
	IsDestructive(input map[string]any) bool

	DefaultPermissions() []PermissionRule
}

// ExecutionContext carries per-invocation context passed to Tool.Call.
type ExecutionContext struct {
	SessionID   string
	AgentID     string
	WorkspaceID string
	WorkingDir  string
}

// ToolResult is the outcome of a tool call.
type ToolResult struct {
	Output       string            `json:"output"`
	IsError      bool              `json:"is_error,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	EnvelopeType string            `json:"envelope_type,omitempty"`
}

// PermissionRule is a default permission declaration on a tool.
type PermissionRule struct {
	Pattern  string `json:"pattern" yaml:"pattern"`
	Behavior string `json:"behavior" yaml:"behavior"` // "allow", "deny", "ask"
}
