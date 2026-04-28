//go:build !devmode

package main

import (
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/muxproxy"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// registerMuxTransport is a no-op in non-devmode builds.
// No mux_* tools are registered; the tool surface is absent in production.
func registerMuxTransport(
	_ *mcp.Manager,
	_ *toolclient.ToolClient,
	_ interface{},
) (*muxproxy.Manager, *service.MuxProxy) {
	return muxproxy.NewManager(), &service.MuxProxy{}
}

// wireMuxPublisher is a no-op in non-devmode builds.
func wireMuxPublisher(_ *muxproxy.Manager, _ *service.StreamManager) {}

// startMuxManager is a no-op in non-devmode builds.
func startMuxManager(_ *lifecycle.Manager, _ *muxproxy.Manager, _ *service.MuxProxy) {}
