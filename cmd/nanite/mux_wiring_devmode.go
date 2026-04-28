//go:build devmode

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/muxproxy"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// registerMuxTransport wires the mux-orchestrator subordinate-agent
// transport into the MCP manager and tool broker.
// POC — CW-20260420-0047. Only active in devmode builds.
func registerMuxTransport(
	mcpManager *mcp.Manager,
	tb *toolclient.ToolClient,
	_ interface{}, // store — not needed here but keeps signatures symmetric
) (*muxproxy.Manager, *service.MuxProxy) {
	muxMgr := muxproxy.NewManager()
	muxSvc := service.NewMuxProxy(muxproxy.Client(), muxMgr)
	muxTransport := muxproxy.NewTransport(muxSvc)
	if err := mcpManager.AddServer("mux-orchestrator", &mcp.MuxTransportAdapter{Inner: muxTransport}, mcp.TierBuiltin); err != nil {
		slog.Error("mcp: failed to register builtin server", "name", "mux-orchestrator", "err", err)
	}

	// Register the four mux_* tools in the tool broker.
	tb.Builtins.RegisterBuiltins("mux-orchestrator", muxproxy.ToolDefinitions())
	slog.Info("registered mux orchestrator built-in tools", "count", len(muxproxy.ToolDefinitions()))

	return muxMgr, muxSvc
}

// wireMuxPublisher sets the StreamPublisher on the mux Manager after the
// StreamManager is available.
func wireMuxPublisher(muxMgr *muxproxy.Manager, streams *service.StreamManager) {
	muxMgr.SetPublisher(muxStreamAdapter{streams: streams})
}

// startMuxManager registers the mux event-fan goroutine in the lifecycle
// manager. POC — CW-20260420-0047.
func startMuxManager(lc *lifecycle.Manager, muxMgr *muxproxy.Manager, muxSvc *service.MuxProxy) {
	lc.Go("mux-manager", func(ctx context.Context) {
		muxMgr.Run(ctx)
		// ctx is already canceled here; use a fresh background ctx with a short
		// timeout so StopSession calls can actually reach the daemon.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		muxSvc.StopAll(cleanupCtx)
	})
}

// muxStreamAdapter adapts *service.StreamManager to the muxproxy.StreamPublisher
// interface. It converts muxproxy.SubEvent → chat.StreamEvent and calls
// BroadcastSessionStreamEvent. CW-20260420-0047.
type muxStreamAdapter struct {
	streams *service.StreamManager
}

func (a muxStreamAdapter) PublishSubEvent(sessionID string, evt muxproxy.SubEvent) {
	sev := chat.StreamEvent{
		Type:    evt.Type,
		Content: evt.Content,
		AgentID: evt.AgentID,
		Tool:    evt.Tool,
	}
	if evt.InputTokens != 0 || evt.OutputTokens != 0 {
		sev.Usage = &chat.Usage{
			InputTokens:  evt.InputTokens,
			OutputTokens: evt.OutputTokens,
		}
	}
	a.streams.BroadcastSessionStreamEvent(sessionID, sev)
}
