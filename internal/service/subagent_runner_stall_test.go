package service

import (
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
)

// TestDrainCapture_FastFailsOnErrorEvent confirms the working half of the
// CW-20260516-0061 boundary: when the child chat loop emits an actual
// `error` event, drainCapture returns immediately with errStreamFailure,
// without waiting for the channel to close. A provider error that surfaces
// promptly already fast-fails — that path is NOT the bug.
func TestDrainCapture_FastFailsOnErrorEvent(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "opening line"}
	ch <- chat.StreamEvent{Type: "error", Error: "Streaming error from provider"}
	// Channel intentionally left open — drainCapture must return on the
	// error event itself, not on a subsequent close.

	done := make(chan struct{})
	var gotErr error
	go func() {
		_, _, _, gotErr = drainCapture(ch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drainCapture did not return on the error event")
	}
	if !errors.Is(gotErr, errStreamFailure) {
		t.Fatalf("expected errStreamFailure, got %v", gotErr)
	}
}

// TestDrainCapture_NoIdleTimeout_OnSilentStall is the CW-20260516-0061
// repro / characterization. It pins the root cause: drainCapture has NO
// inactivity timeout. When the provider stream stalls silently — an
// opening delta, then no further events and no channel close (c242: "the
// child agent emitted only an opening line") — drainCapture blocks
// indefinitely.
//
// CW-20260519-0073 update: the run-level ctx deadline that bounds this
// block is no longer a fixed 300s wall clock. The subagent run's
// governing liveness signal is now the child chat loop's *inactivity*
// timeout (shouldStop Layer 2, scoped to subagent dispatch via
// subagentIdleTimeoutSeconds — 300s of silence). The chat loop emits a
// clean TerminationIdleTimeout envelope when no activity is observed for
// that window; subagent.Service.execute still applies a generous
// context.WithTimeout (DefaultTimeoutSeconds = 1800s) as a pure
// backstop. drainCapture itself is unchanged — it has no internal
// inactivity bound; that bound lives in the chat loop upstream of it.
//
// If a future change adds a provider-stream inactivity timeout inside
// drainCapture (see docs/cw-20260516-0061-300s-hang-findings.md), this
// test must be updated to assert the new bounded return instead of an
// indefinite block.
func TestDrainCapture_NoIdleTimeout_OnSilentStall(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "opening line"}
	// No further events; channel never closed — a silent provider stall.

	done := make(chan struct{})
	go func() {
		_, _, _, _ = drainCapture(ch)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("drainCapture returned on its own — an inactivity timeout " +
			"appears to have been added; update this characterization test " +
			"and docs/cw-20260516-0061-300s-hang-findings.md")
	case <-time.After(750 * time.Millisecond):
		// Expected: drainCapture is still blocked. Root cause confirmed —
		// no bound shorter than the run-level ctx deadline exists.
	}
	// Unblock the leaked goroutine so the test process stays clean.
	close(ch)
	<-done
}
