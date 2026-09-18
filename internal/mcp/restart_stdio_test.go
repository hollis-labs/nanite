package mcp

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// registerFixtureStdioServer registers a stdio server backed by this test
// binary re-exec'd as a real MCP server (see stdio_fixture_test.go) --
// real enough to prove a connect-restart-respawn cycle works end to end
// through Manager's own wiring, without needing a hand-rolled protocol
// simulation.
func registerFixtureStdioServer(t *testing.T, mgr *Manager, name string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if err := mgr.AddStdioServer(name, exe, nil, []string{runAsFixtureServerEnv + "=1"}, []string{"PATH"}, TierBuiltin); err != nil {
		t.Fatalf("AddStdioServer: %v", err)
	}
}

// TestRestartStdioTransports_RespawnsAfterRestart drives a real stdio
// subprocess through a full connect → restart → reconnect cycle: reaping
// the wedged process and reconnecting on the next call is now
// go-mcp/client's own tested responsibility (see its stdio_test.go); what's
// under test here is that Manager's RestartStdioTransports correctly finds
// and invalidates the registered server, and that the next call through
// Manager's own API transparently respawns.
func TestRestartStdioTransports_RespawnsAfterRestart(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(mgr.Close)
	registerFixtureStdioServer(t, mgr, "test-stdio")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := mgr.DiscoverServerTools(ctx, "test-stdio"); err != nil {
		t.Fatalf("initial DiscoverServerTools: %v", err)
	}

	if err := mgr.RestartStdioTransports(context.Background()); err != nil {
		t.Fatalf("RestartStdioTransports: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if _, err := mgr.DiscoverServerTools(ctx2, "test-stdio"); err != nil {
		t.Fatalf("DiscoverServerTools after restart: %v", err)
	}
}

// TestRestartStdioTransports_Idempotent ensures back-to-back calls do not
// panic or block, cycling a connected subprocess twice in a row.
func TestRestartStdioTransports_Idempotent(t *testing.T) {
	mgr := NewManager()
	t.Cleanup(mgr.Close)
	registerFixtureStdioServer(t, mgr, "test-stdio")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := mgr.DiscoverServerTools(ctx, "test-stdio"); err != nil {
		t.Fatalf("initial DiscoverServerTools: %v", err)
	}

	if err := mgr.RestartStdioTransports(context.Background()); err != nil {
		t.Fatalf("first RestartStdioTransports: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- mgr.RestartStdioTransports(context.Background())
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second RestartStdioTransports: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second RestartStdioTransports blocked")
	}
}

// TestRestartStdioTransports_RespectsContextCancel verifies the call
// returns ctx.Err() promptly when the caller's context is already
// canceled. The broker's Remediate runs under a 10s ctx; an already-
// expired ctx must not block on a real subprocess Close.
func TestRestartStdioTransports_RespectsContextCancel(t *testing.T) {
	mgr := NewManager()
	// No transports registered — exercises the fast-path ctx.Err() check
	// at the top of RestartStdioTransports without spawning anything.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mgr.RestartStdioTransports(ctx)
	if err == nil {
		t.Fatal("RestartStdioTransports: expected ctx.Err(), got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RestartStdioTransports: err = %v, want context.Canceled", err)
	}
}

// TestRestartStdioTransports_NoStdioTransports verifies the manager-side
// vacuous-success path: a manager with only HTTP / builtin transports
// returns nil (no stdio subprocesses to cycle).
func TestRestartStdioTransports_NoStdioTransports(t *testing.T) {
	mgr := NewManager()
	// AddServer with a non-stdio transport to confirm RestartStdioTransports
	// skips it without error. Use a self-tools-style in-process transport;
	// DevToolsTransport with empty allowlist works.
	if err := mgr.AddServer("dev", NewDevToolsTransport(nil), TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	if err := mgr.RestartStdioTransports(context.Background()); err != nil {
		t.Fatalf("RestartStdioTransports: %v", err)
	}
}
