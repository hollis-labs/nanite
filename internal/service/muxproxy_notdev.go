//go:build !devmode

package service

import (
	"context"

	agentmux "github.com/hollis-labs/go-agentmux-client"
	"github.com/hollis-labs/nanite/internal/muxproxy"
)

// MuxProxy is a no-op stub in non-devmode builds. The mux-orchestrator
// POC is fully absent from production binaries.
type MuxProxy struct{}

// NewMuxProxy returns an empty stub. In non-devmode builds the muxproxy
// package exports no-op types so this constructor satisfies any callers
// that are also gated behind build-tagged helpers.
func NewMuxProxy(_ *agentmux.Client, _ *muxproxy.Manager) *MuxProxy {
	return &MuxProxy{}
}

// StopAll is a no-op in non-devmode builds.
func (s *MuxProxy) StopAll(_ context.Context) {}
