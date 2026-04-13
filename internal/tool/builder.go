package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// CallFunc is the function signature for tool execution.
type CallFunc func(ctx context.Context, input map[string]any, execCtx ExecutionContext) (*ToolResult, error)

// ToolOption configures a tool created via NewTool.
type ToolOption func(*toolImpl)

// NewTool creates a Tool from a name, description, and functional options.
//
// Defaults: category="", source=SourceBuiltin, not concurrent-safe,
// not read-only, not destructive, no timeout (system default applies),
// no permission rules, no tags.
func NewTool(name, description string, opts ...ToolOption) Tool {
	t := &toolImpl{
		name:        name,
		description: description,
		source:      SourceBuiltin,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// --- Option functions ---

func WithCategory(c string) ToolOption {
	return func(t *toolImpl) { t.category = c }
}

func WithSchema(s json.RawMessage) ToolOption {
	return func(t *toolImpl) { t.inputSchema = s }
}

// WithSchemaMap marshals a map to json.RawMessage for convenience.
// Existing tools define schemas as map[string]any; this avoids forcing
// pre-marshaling at every call site.
func WithSchemaMap(s map[string]any) ToolOption {
	return func(t *toolImpl) {
		data, err := json.Marshal(s)
		if err != nil {
			slog.Error("tool: WithSchemaMap marshal error", "name", t.name, "err", err)
			return
		}
		t.inputSchema = data
	}
}

func WithSource(s string) ToolOption {
	return func(t *toolImpl) { t.source = s }
}

func WithTags(tags ...string) ToolOption {
	return func(t *toolImpl) { t.tags = tags }
}

func WithTimeout(d time.Duration) ToolOption {
	return func(t *toolImpl) { t.timeout = d }
}

func WithCallFunc(fn CallFunc) ToolOption {
	return func(t *toolImpl) { t.callFn = fn }
}

func WithConcurrencySafe(static bool) ToolOption {
	return func(t *toolImpl) {
		t.concurrencySafeFn = func(map[string]any) bool { return static }
	}
}

func WithConcurrencySafeFunc(fn func(map[string]any) bool) ToolOption {
	return func(t *toolImpl) { t.concurrencySafeFn = fn }
}

func WithReadOnly(static bool) ToolOption {
	return func(t *toolImpl) {
		t.readOnlyFn = func(map[string]any) bool { return static }
	}
}

func WithReadOnlyFunc(fn func(map[string]any) bool) ToolOption {
	return func(t *toolImpl) { t.readOnlyFn = fn }
}

func WithDestructive(static bool) ToolOption {
	return func(t *toolImpl) {
		t.destructiveFn = func(map[string]any) bool { return static }
	}
}

func WithDestructiveFunc(fn func(map[string]any) bool) ToolOption {
	return func(t *toolImpl) { t.destructiveFn = fn }
}

func WithPermissions(rules ...PermissionRule) ToolOption {
	return func(t *toolImpl) { t.permissions = rules }
}

func WithValidateFunc(fn func(map[string]any) error) ToolOption {
	return func(t *toolImpl) { t.validateFn = fn }
}

// --- toolImpl ---

// toolImpl is the concrete implementation backing NewTool.
type toolImpl struct {
	name        string
	description string
	category    string
	inputSchema json.RawMessage
	source      string
	tags        []string
	timeout     time.Duration

	callFn             CallFunc
	validateFn         func(map[string]any) error
	concurrencySafeFn  func(map[string]any) bool
	readOnlyFn         func(map[string]any) bool
	destructiveFn      func(map[string]any) bool
	permissions        []PermissionRule
}

func (t *toolImpl) Name() string               { return t.name }
func (t *toolImpl) Description() string         { return t.description }
func (t *toolImpl) Category() string            { return t.category }
func (t *toolImpl) Source() string              { return t.source }
func (t *toolImpl) Timeout() time.Duration      { return t.timeout }

func (t *toolImpl) InputSchema() json.RawMessage {
	if t.inputSchema == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.inputSchema
}

func (t *toolImpl) Tags() []string {
	if t.tags == nil {
		return []string{}
	}
	out := make([]string, len(t.tags))
	copy(out, t.tags)
	return out
}

func (t *toolImpl) Call(ctx context.Context, input map[string]any, execCtx ExecutionContext) (*ToolResult, error) {
	if t.callFn == nil {
		return nil, fmt.Errorf("tool %q has no call function", t.name)
	}
	return t.callFn(ctx, input, execCtx)
}

func (t *toolImpl) ValidateInput(input map[string]any) error {
	if t.validateFn == nil {
		return nil
	}
	return t.validateFn(input)
}

func (t *toolImpl) IsConcurrencySafe(input map[string]any) bool {
	if t.concurrencySafeFn == nil {
		return false
	}
	return t.concurrencySafeFn(input)
}

func (t *toolImpl) IsReadOnly(input map[string]any) bool {
	if t.readOnlyFn == nil {
		return false
	}
	return t.readOnlyFn(input)
}

func (t *toolImpl) IsDestructive(input map[string]any) bool {
	if t.destructiveFn == nil {
		return false
	}
	return t.destructiveFn(input)
}

func (t *toolImpl) DefaultPermissions() []PermissionRule {
	if t.permissions == nil {
		return []PermissionRule{}
	}
	return t.permissions
}
