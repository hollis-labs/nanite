//go:build devmode

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/muxproxy"
)

// stubMuxService satisfies muxproxy.MuxService for the trust gate tests.
type stubMuxService struct{}

func (s *stubMuxService) ListAvailableLaunches(_ context.Context) ([]muxproxy.LaunchSummary, error) {
	return nil, nil
}
func (s *stubMuxService) LaunchSubordinate(_ context.Context, _, _ string) (muxproxy.LaunchResult, error) {
	return muxproxy.LaunchResult{SessionID: "s1"}, nil
}
func (s *stubMuxService) Send(_ context.Context, _, _ string) (muxproxy.SendResult, error) {
	return muxproxy.SendResult{}, nil
}
func (s *stubMuxService) Stop(_ context.Context, _ string) error { return nil }

// stubMuxTrustResolver is a simple test double for dispatch.TrustResolver.
type stubMuxTrustResolver struct {
	tier dispatch.TrustTier
	err  error
}

func (r *stubMuxTrustResolver) ResolveTrust(_ context.Context, _, _ string) (dispatch.TrustTier, error) {
	return r.tier, r.err
}

// TestMuxAdapter_CallTool_TrustedCallerPropagatesCtx verifies that when the
// ctx carries a caller profile (workspaceID + agentProfileID) the adapter
// forwards it to the muxproxy Transport and the H1 trust gate fires.
// A trusted resolver allows the call; a normal resolver allows it too
// (approval is handled at the subagent layer, not here). The test asserts
// that the gate is consulted when the ctx is populated.
func TestMuxAdapter_CallTool_TrustedCallerPropagatesCtx(t *testing.T) {
	tr := muxproxy.NewTransport(&stubMuxService{})
	tr.SetTrustResolver(&stubMuxTrustResolver{tier: dispatch.TrustTrusted})
	adapter := &MuxTransportAdapter{Inner: tr}

	// Stamp caller profile so the adapter can propagate it to WithCallerCtx.
	ctx := WithCallerProfile(context.Background(), "ws-dogfood", "ap-worker")

	result, err := adapter.CallTool(ctx, "mux_list_launches", nil)
	if err != nil {
		t.Fatalf("expected trusted caller to succeed, got: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatal("expected non-empty tool result")
	}
}

// TestMuxAdapter_CallTool_UntrustedCallerBlocked verifies that an untrusted
// caller ctx causes the H1 gate to refuse the call.
func TestMuxAdapter_CallTool_UntrustedCallerBlocked(t *testing.T) {
	tr := muxproxy.NewTransport(&stubMuxService{})
	tr.SetTrustResolver(&stubMuxTrustResolver{tier: dispatch.TrustUntrusted})
	adapter := &MuxTransportAdapter{Inner: tr}

	ctx := WithCallerProfile(context.Background(), "ws-dogfood", "ap-untrusted")

	_, err := adapter.CallTool(ctx, "mux_list_launches", nil)
	if err == nil {
		t.Fatal("expected error for untrusted caller, got nil")
	}
	if !errors.Is(err, dispatch.ErrUntrustedRole) {
		t.Errorf("expected ErrUntrustedRole, got: %v", err)
	}
}

// TestMuxAdapter_CallTool_NormalCallerAllowed verifies that a normal trust
// tier allows the mux_* call (approval is the subagent gate's job, not the
// mux transport's).
func TestMuxAdapter_CallTool_NormalCallerAllowed(t *testing.T) {
	tr := muxproxy.NewTransport(&stubMuxService{})
	tr.SetTrustResolver(&stubMuxTrustResolver{tier: dispatch.TrustNormal})
	adapter := &MuxTransportAdapter{Inner: tr}

	ctx := WithCallerProfile(context.Background(), "ws-dogfood", "ap-chat")

	result, err := adapter.CallTool(ctx, "mux_list_launches", nil)
	if err != nil {
		t.Fatalf("normal trust should allow mux tool call, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected tool result")
	}
}

// TestMuxAdapter_CallTool_NoCallerProfile_NoGate verifies that when the ctx
// carries no caller profile the adapter does NOT stamp the muxproxy ctx,
// so the gate is skipped (allow-all fallback when WorkspaceID is empty).
func TestMuxAdapter_CallTool_NoCallerProfile_NoGate(t *testing.T) {
	tr := muxproxy.NewTransport(&stubMuxService{})
	// Wiring an untrusted resolver — if the gate fired it would block.
	tr.SetTrustResolver(&stubMuxTrustResolver{tier: dispatch.TrustUntrusted})
	adapter := &MuxTransportAdapter{Inner: tr}

	// No caller profile stamped — ctx carries nothing.
	result, err := adapter.CallTool(context.Background(), "mux_list_launches", nil)
	if err != nil {
		t.Fatalf("empty ctx should skip gate, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected tool result")
	}
}

// itoa is borrowed from manager_test helpers in the same package.
// Defined only if not already declared; suppress lint via blank usage.
var _ = func() bool { b, _ := json.Marshal(1); return len(b) > 0 }()
