package service

import (
	"context"
	"errors"
	"testing"
)

// TestRebootSessionAgent_EmptySessionID rejects a blank id outright.
func TestRebootSessionAgent_EmptySessionID(t *testing.T) {
	s := &chatServiceImpl{}
	if _, err := s.RebootSessionAgent(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty session id, got nil")
	}
}

// TestRebootSessionAgent_NoActiveAgent: with no runtime session tracked in
// activeSessions, the reboot is a no-op success — the next user turn
// cold-boots a fresh agent anyway, so the caller's intent is satisfied.
func TestRebootSessionAgent_NoActiveAgent(t *testing.T) {
	s := &chatServiceImpl{}
	res, err := s.RebootSessionAgent(context.Background(), "sess-none")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Rebooted {
		t.Errorf("Rebooted = true, want false (no live agent)")
	}
	if res.Status != "no_active_agent" {
		t.Errorf("Status = %q, want no_active_agent", res.Status)
	}
}

// TestRebootSessionAgent_RejectsInFlightTurn is the load-bearing guard:
// rebooting under a streaming turn would yank the runtime out from under an
// active streamLoop, so RebootSessionAgent must return ErrSessionBusy when a
// generateResponse is registered for the session. A different, idle session
// must remain unaffected.
func TestRebootSessionAgent_RejectsInFlightTurn(t *testing.T) {
	s := &chatServiceImpl{activeGen: map[string]*inFlightGen{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.registerGeneration("sess-busy", "msg-1", cancel)

	if _, err := s.RebootSessionAgent(ctx, "sess-busy"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("expected ErrSessionBusy for an in-flight turn, got %v", err)
	}

	res, err := s.RebootSessionAgent(ctx, "sess-idle")
	if err != nil {
		t.Fatalf("unexpected error for an idle session: %v", err)
	}
	if res.Status != "no_active_agent" {
		t.Errorf("idle session Status = %q, want no_active_agent", res.Status)
	}
}
