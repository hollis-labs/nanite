package service

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
)

// TestRecoveryMCPAdapter_RealManager_NotWiredErrorClosed is the
// integration smoke for the Phase 9 MCP wiring (CW-20260510-0015).
//
// Pre-Phase-9, broker.Dependencies.MCP was nil and Broker.Remediate
// surfaced the literal sentinel "broker.Remediate: MCP not wired"
// for any RemediationRefreshMCPTransport classification. This test
// constructs a real *mcp.Manager, wraps it in recoveryMCPAdapter, hands
// the adapter to broker.NewBroker, and drives Remediate with a
// RemediationRefreshMCPTransport classification. The assertion: the
// returned error is NOT the "MCP not wired" sentinel — proving the
// wiring is in place. The remediation itself is a no-op against an
// idle stdio transport (no subprocess started yet), so success is
// nil-error.
func TestRecoveryMCPAdapter_RealManager_NotWiredErrorClosed(t *testing.T) {
	mgr := mcp.NewManager()

	// Register a real stdio server so RestartStdioTransports has something
	// to iterate. We never issue an actual call, so the underlying
	// go-mcp/client connection is never dialed — Invalidate on a
	// never-connected server is a no-op. The `sleep` shape mirrors the
	// harness used by go-mcp/client's own stdio leak-regression tests.
	if _, err := exec.LookPath("sleep"); err == nil {
		if err := mgr.AddStdioServer("test-stdio", "sleep", []string{"300"}, nil, []string{"PATH"}, mcp.TierBuiltin); err != nil {
			t.Fatalf("AddStdioServer: %v", err)
		}
	}

	adapter := &recoveryMCPAdapter{manager: mgr}

	// Sanity check: adapter forwards into the manager directly.
	if err := adapter.RestartTransport(context.Background(), "sess-smoke"); err != nil {
		t.Fatalf("adapter.RestartTransport: unexpected err = %v", err)
	}

	// Drive broker.Broker.Remediate end-to-end with a
	// RemediationRefreshMCPTransport classification. The broker dispatches
	// into deps.MCP.RestartTransport which forwards into our adapter.
	b := broker.NewBroker(broker.Dependencies{
		MCP: adapter,
	})
	ev := &broker.FailureEvent{SessionID: "sess-smoke"}
	cls := broker.Classification{
		Class:       broker.ClassConfigPermissions,
		Reason:      "MCP transport down — restart before retrying",
		Remediation: broker.RemediationRefreshMCPTransport,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := b.Remediate(ctx, ev, cls)
	if err != nil {
		// Critical: the legacy "MCP not wired" branch must not fire.
		if strings.Contains(err.Error(), "MCP not wired") {
			t.Fatalf("recovery: MCP still reports as not wired — wiring regression: %v", err)
		}
		t.Fatalf("Remediate: unexpected err = %v", err)
	}
}

// TestRecoveryMCPAdapter_RealManager_PropagatesUnderlyingError verifies
// the error-wrap path through the real broker dispatch. We simulate a
// failing manager via a minimal stub satisfying mcpTransportRestarter,
// then assert errors.Is sees through the broker -> adapter layers.
func TestRecoveryMCPAdapter_RealManager_PropagatesUnderlyingError(t *testing.T) {
	sentinel := errors.New("simulated stdio close failure")
	stub := &fakeTransportRestarter{err: sentinel}
	adapter := &recoveryMCPAdapter{manager: stub}

	b := broker.NewBroker(broker.Dependencies{
		MCP: adapter,
	})
	ev := &broker.FailureEvent{SessionID: "sess-fail"}
	cls := broker.Classification{
		Class:       broker.ClassConfigPermissions,
		Reason:      "MCP transport down — restart before retrying",
		Remediation: broker.RemediationRefreshMCPTransport,
	}

	err := b.Remediate(context.Background(), ev, cls)
	if err == nil {
		t.Fatal("Remediate: expected propagated error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Remediate err = %v, want errors.Is(%v) = true", err, sentinel)
	}
	if got := stub.count.Load(); got != 1 {
		t.Fatalf("manager invocation count = %d, want 1", got)
	}
}
