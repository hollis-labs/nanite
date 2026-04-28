//go:build !devmode

package mcp

import (
	"context"

	"github.com/hollis-labs/nanite/internal/muxproxy"
)

// MuxTransportAdapter is a no-op stub in non-devmode builds. The
// mux-orchestrator POC is fully absent from production binaries.
// The type is defined here so main.go can reference it in the
// build-tagged mux wiring helper without a type-unknown error.
type MuxTransportAdapter struct {
	Inner *muxproxy.Transport
}

// ListTools returns an empty slice in non-devmode builds.
func (a *MuxTransportAdapter) ListTools(_ context.Context) ([]Tool, error) {
	return nil, nil
}

// CallTool is unreachable in non-devmode builds because mux_* tools are
// never registered. Returns nil unconditionally.
func (a *MuxTransportAdapter) CallTool(_ context.Context, _ string, _ map[string]any) (*ToolResult, error) {
	return nil, nil
}
