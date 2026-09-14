package mcp

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// TestRestartStdioTransports_ReapsRunningSubprocess drives a real
// `sleep` subprocess registered as a stdio MCP transport and verifies
// RestartStdioTransports reaps it. After the call the transport's
// started flag is cleared so the next call() lazily respawns.
func TestRestartStdioTransports_ReapsRunningSubprocess(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skipf("sleep not on PATH: %v", err)
	}

	mgr := NewManager()
	tr := newHandshakingStubTransport(t)
	if err := mgr.AddServer("test-stdio", tr, TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	// Force the subprocess to start by calling start() under lock —
	// we don't want to actually issue a JSON-RPC call (no real server)
	// and ListTools would block on the read.
	tr.mu.Lock()
	if err := tr.start(); err != nil {
		tr.mu.Unlock()
		t.Fatalf("start: %v", err)
	}
	if !tr.started {
		tr.mu.Unlock()
		t.Fatal("transport did not mark as started after start()")
	}
	tr.mu.Unlock()

	if err := mgr.RestartStdioTransports(context.Background()); err != nil {
		t.Fatalf("RestartStdioTransports: %v", err)
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.started {
		t.Fatal("transport still marked started after RestartStdioTransports")
	}
	if tr.cmd == nil || tr.cmd.ProcessState == nil {
		t.Fatal("subprocess was not waited on — ProcessState nil; would leak")
	}
}

// TestRestartStdioTransports_Idempotent ensures back-to-back calls do
// not panic or block. Cycles a started subprocess, then calls Restart a
// second time on the (already-reaped) transport — Close inside the
// manager is a no-op when !started, so the second call must succeed
// quietly.
func TestRestartStdioTransports_Idempotent(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skipf("sleep not on PATH: %v", err)
	}

	mgr := NewManager()
	tr := newHandshakingStubTransport(t)
	if err := mgr.AddServer("test-stdio", tr, TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	tr.mu.Lock()
	if err := tr.start(); err != nil {
		tr.mu.Unlock()
		t.Fatalf("start: %v", err)
	}
	tr.mu.Unlock()

	if err := mgr.RestartStdioTransports(context.Background()); err != nil {
		t.Fatalf("first RestartStdioTransports: %v", err)
	}

	// Second call must not panic / block / error — *StdioTransport.Close
	// is idempotent via killAndReapLocked when !started.
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
