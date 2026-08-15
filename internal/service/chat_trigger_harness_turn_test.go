package service

// Regression tests for PR #247's review comment: TriggerHarnessTurn must
// use reject-if-busy semantics (registerGenerationIfIdle), not
// launchGeneration's takeover semantics — a harness-triggered turn must
// never cancel a real user's in-flight generation, including under the
// TOCTOU race where a user turn starts between the reactor's IsGenerating
// pre-check and this call.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/lifecycle"
)

func newTriggerHarnessTurnTestService(t *testing.T) (*chatServiceImpl, *capturingStore) {
	t.Helper()
	cs := &capturingStore{}
	return &chatServiceImpl{
		store:     cs,
		streams:   NewStreamManager(),
		lifecycle: lifecycle.NewManager("test.trigger-harness-turn"),
		activeGen: make(map[string]*inFlightGen),
	}, cs
}

func TestTriggerHarnessTurn_BusySession_RejectsWithoutInterrupting(t *testing.T) {
	svc, cs := newTriggerHarnessTurnTestService(t)

	cancelled := false
	svc.activeGen["sess-1"] = &inFlightGen{msgID: "user-turn-in-flight", cancel: func() { cancelled = true }}

	_, err := svc.TriggerHarnessTurn(context.Background(), "sess-1", "subagent_completion", "run-1")

	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("expected ErrSessionBusy, got %v", err)
	}
	if cancelled {
		t.Error("TriggerHarnessTurn must not cancel the existing in-flight user generation (takeover semantics would do this — reject-if-busy must not)")
	}
	if cs.callCount != 0 {
		t.Errorf("expected no message created when session is busy, got %d CreateMessage calls", cs.callCount)
	}
	if got := svc.activeGen["sess-1"]; got == nil || got.msgID != "user-turn-in-flight" {
		t.Errorf("expected the original in-flight generation slot to remain untouched, got %+v", got)
	}
}

func TestTriggerHarnessTurn_IdleSession_Succeeds(t *testing.T) {
	svc, cs := newTriggerHarnessTurnTestService(t)

	msgID, err := svc.TriggerHarnessTurn(context.Background(), "sess-1", "subagent_completion", "run-1")
	if err != nil {
		t.Fatalf("TriggerHarnessTurn: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty assistant message id")
	}
	if cs.callCount != 1 {
		t.Errorf("expected 1 message created, got %d", cs.callCount)
	}
}

// TestRegisterGenerationIfIdle_ConcurrentCallers_ExactlyOneWins is the
// direct atomicity test for the TOCTOU fix: this is the primitive
// TriggerHarnessTurn relies on to close the race where a real user turn
// starts between the reactor's IsGenerating pre-check and the call to
// TriggerHarnessTurn. (A full end-to-end race through TriggerHarnessTurn
// itself is not reliably reproducible in a unit test: with no dispatcher
// wired, the losing/winning goroutine's generation completes and
// self-deregisters near-instantly, so the "busy" window is too narrow to
// race against deterministically — the atomicity has to be proven at the
// primitive that creates it.)
func TestRegisterGenerationIfIdle_ConcurrentCallers_ExactlyOneWins(t *testing.T) {
	svc, _ := newTriggerHarnessTurnTestService(t)

	const n = 50
	var wg sync.WaitGroup
	var wins int32
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if svc.registerGenerationIfIdle("sess-1", "msg", func() {}) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("expected exactly 1 of %d concurrent registerGenerationIfIdle callers to win, got %d", n, wins)
	}
}
