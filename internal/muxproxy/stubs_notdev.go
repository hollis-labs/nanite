//go:build !devmode

// Package muxproxy is a POC package gated behind the devmode build tag.
// In production builds this file provides no-op stubs so callers compile
// cleanly without any mux_* tool surface or agent-profile seeding.
package muxproxy

import (
	"context"
	"encoding/json"

	agentmux "github.com/hollis-labs/go-agentmux-client"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/dispatch"
)

// Manager is a no-op stub in non-devmode builds.
type Manager struct{}

// NewManager returns an empty Manager. No-op in non-devmode builds.
func NewManager() *Manager { return &Manager{} }

// NewManagerWithStream returns an empty Manager. No-op stub.
func NewManagerWithStream(_ interface{}) *Manager { return &Manager{} }

// SetPublisher is a no-op in non-devmode builds.
func (m *Manager) SetPublisher(_ StreamPublisher) {}

// Run blocks until ctx is cancelled. No subordinate sessions are managed
// in non-devmode builds.
func (m *Manager) Run(ctx context.Context) { <-ctx.Done() }

// StopAll returns nil; no sessions to stop in non-devmode builds.
func (m *Manager) StopAll() []string { return nil }

// StopAllForChat returns nil.
func (m *Manager) StopAllForChat(_ string) []string { return nil }

// Register is a no-op and returns a closed channel.
func (m *Manager) Register(_, _, _ string) <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// Unregister is a no-op.
func (m *Manager) Unregister(_ string) {}

// Nickname returns "" always.
func (m *Manager) Nickname(_ string) string { return "" }

// WaiterChannel returns nil.
func (m *Manager) WaiterChannel(_ string) chan struct{} { return nil }

// Transport is a no-op stub in non-devmode builds.
type Transport struct{}

// NewTransport returns an empty Transport.
func NewTransport(_ MuxService) *Transport { return &Transport{} }

// SetTrustResolver is a no-op in non-devmode builds.
func (t *Transport) SetTrustResolver(_ dispatch.TrustResolver) {}

// ListTools returns an empty slice.
func (t *Transport) ListTools(_ context.Context) ([]ToolDef, error) { return nil, nil }

// CallTool always returns an error in non-devmode builds; the transport
// should never be reached because the tools are not registered.
func (t *Transport) CallTool(_ context.Context, name string, _ map[string]any) (json.RawMessage, error) {
	return nil, nil //nolint:nilnil // stub; transport never registered in non-devmode
}

// ToolDef mirrors the shape used by mcp.MuxTransportAdapter.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// MuxService is the subset of service.MuxProxy the Transport depends on.
// Kept here so mcp.MuxTransportAdapter compiles in non-devmode.
type MuxService interface {
	ListAvailableLaunches(ctx context.Context) ([]LaunchSummary, error)
	LaunchSubordinate(ctx context.Context, launchID, nickname string) (LaunchResult, error)
	Send(ctx context.Context, sessionID, text string) (SendResult, error)
	Stop(ctx context.Context, sessionID string) error
}

// SubEvent is a no-op stub so muxStreamAdapter in main.go compiles.
type SubEvent struct {
	Type         string
	Content      string
	AgentID      string
	InputTokens  int
	OutputTokens int
	Tool         string
}

// StreamPublisher stub so muxStreamAdapter satisfies the interface.
type StreamPublisher interface {
	PublishSubEvent(sessionID string, evt SubEvent)
}

// WithCallerCtx returns ctx unchanged in non-devmode builds.
// The H1 trust gate does not exist; context stamping is a no-op.
func WithCallerCtx(ctx context.Context, _, _ string) context.Context { return ctx }

// ToolDefinitions returns an empty slice in non-devmode builds.
// Production tool surface contains no mux_* tools.
func ToolDefinitions() []provider.ToolDefinition { return []provider.ToolDefinition{} }

// Client returns nil in non-devmode builds. The return type matches the
// devmode signature so call sites in build-tagged helpers compile cleanly.
func Client() *agentmux.Client { return nil }

// LaunchSummary mirrors service.LaunchSummary.
type LaunchSummary struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Agent    string `json:"agent"`
	Provider string `json:"provider"`
}

// LaunchResult mirrors service.LaunchResult.
type LaunchResult struct {
	SessionID  string `json:"session_id"`
	ProviderID string `json:"provider_id"`
	Nickname   string `json:"nickname"`
}

// SendToolUse mirrors service.SendToolUse.
type SendToolUse struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// SendResult mirrors service.SendResult.
type SendResult struct {
	Transcript   string        `json:"transcript"`
	ToolUses     []SendToolUse `json:"tool_uses"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
	ExitStatus   string        `json:"exit_status"`
	Error        string        `json:"error,omitempty"`
}
