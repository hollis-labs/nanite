package chat

import (
	"testing"
)

// The envelope-response role-remap behavior this file used to cover via the
// removed legacy AssembleContext path (CW-20260814-0005) is now covered at
// the e2e level against the live AssembleSlotSources path — see
// TestEnvelopeRespond_E2E_TranscriptThreadedIntoContext /
// TestEnvelopeRespond_E2E_SilentHandlerNotInContext in
// internal/api/envelopes_e2e_test.go.

func TestFormatEnvelopeResponseContent(t *testing.T) {
	got := FormatEnvelopeResponseContent("q", StatusCancelled, "")
	want := "[envelope:q status:cancelled] {}"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
