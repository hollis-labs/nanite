package service

import (
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/subagent"
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
// backstop.
//
// CW-20260517-0036 update: the within-stream silent-stall gap that
// CW-20260519-0073 explicitly left open is now closed — but NOT inside
// drainCapture. drainCapture is a pure parsing function with no timer
// and is deliberately left that way. The provider-stream inactivity
// timeout lives one layer up, in generateResponse's `streamLoop` (a
// select with a resettable timer over the provider event channel): a
// silently stalled provider stream is now cancelled there, which closes
// the capture channel, which is what unblocks drainCapture. So this
// characterization still holds for the isolated-channel scenario it
// constructs — drainCapture in isolation, with no upstream stream loop
// feeding it, genuinely has no inactivity bound. The real production
// stall is bounded upstream; this test pins that drainCapture itself is
// not the layer that bounds it.
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

// TestDrainCapture_StalledErrorEventJoinsErrStalled is the CW-20260519-0074
// acceptance test for the stall-classification signal. The provider-stream
// inactivity watchdog (CW-20260517-0036) emits its terminal error event
// with a structured `cause:"stalled"` detail. drainCapture must join
// subagent.ErrStalled into the returned error so the run-outcome
// classifier in subagent.execute can errors.Is it and stamp StatusStalled.
func TestDrainCapture_StalledErrorEventJoinsErrStalled(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "delta", Content: "partial"}
	// Mirror chat_generate.go's stalled-branch ErrorEvent: a structured
	// error whose Details carry cause:"stalled".
	stallEvt := chat.ErrorEvent(chat.ErrorCodeProviderError,
		"Provider stream stalled — no response",
		map[string]interface{}{"cause": "stalled", "inactivity_window": "5m0s"})
	ch <- stallEvt
	close(ch)

	_, _, _, err := drainCapture(ch)
	if err == nil {
		t.Fatal("expected error from drainCapture on stalled event, got nil")
	}
	if !errors.Is(err, errStreamFailure) {
		t.Errorf("error = %v, want wrapped errStreamFailure", err)
	}
	if !errors.Is(err, subagent.ErrStalled) {
		t.Errorf("error = %v, want wrapped subagent.ErrStalled (stall classification signal)", err)
	}
}

// TestDrainCapture_GenericErrorEventOmitsErrStalled confirms the negative
// case: a provider-emitted error WITHOUT a cause:"stalled" detail must NOT
// carry subagent.ErrStalled, so the classifier keeps it `failed` rather
// than mislabeling a crash as a stall.
func TestDrainCapture_GenericErrorEventOmitsErrStalled(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	// A plain error event (no StructuredError) — the legacy shape.
	ch <- chat.StreamEvent{Type: "error", Error: "provider exploded"}
	close(ch)
	_, _, _, err := drainCapture(ch)
	if err == nil {
		t.Fatal("expected error from drainCapture, got nil")
	}
	if errors.Is(err, subagent.ErrStalled) {
		t.Errorf("error = %v, must NOT carry subagent.ErrStalled for a generic provider error", err)
	}

	// A structured error with a non-stall cause must also be omitted.
	ch2 := make(chan chat.StreamEvent, 4)
	ch2 <- chat.ErrorEvent(chat.ErrorCodeProviderError, "Provider streaming failed",
		map[string]interface{}{"raw": "boom", "model": "x"})
	close(ch2)
	_, _, _, err2 := drainCapture(ch2)
	if errors.Is(err2, subagent.ErrStalled) {
		t.Errorf("error = %v, must NOT carry subagent.ErrStalled for a non-stall structured error", err2)
	}
}
