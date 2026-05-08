package recovery

import (
	"crypto/rand"
	"encoding/hex"
)

// RenderUserMessage builds the envelope the broker emits for a given
// (FailureEvent, Classification, Action) triple. Maps onto the existing
// envelope kinds — info-card for retries, error-report or
// chat-loop-terminated for permanent failures. No new envelope schemas.
//
// Phase 1: skeleton + the (Action, Kind) routing table from the
// implementer prompt. Phase 5 fills out Title/Content with the real
// rendering logic + cancel-token wiring.
func (b *Broker) RenderUserMessage(ev *FailureEvent, c Classification, action Action) Envelope {
	switch action {
	case ActionRetryTransient:
		return Envelope{
			Kind:        "info-card",
			Title:       "Reconnecting agent",
			Content:     "Agent ran into a temporary error. Retrying…",
			CancelToken: newCancelToken(),
			Severity:    "info",
		}

	case ActionRetryConfigFixed:
		return Envelope{
			Kind:        "info-card",
			Title:       "Fixed an agent issue",
			Content:     "Resolved a configuration issue; retrying…", // Phase 5: thread Remediation.String() into the message
			CancelToken: newCancelToken(),
			Severity:    "info",
		}

	case ActionPermanentFailure:
		// Phase 5 will switch between error-report (default) and
		// chat-loop-terminated (when Cause == restart_exhausted or the
		// failure has a runaway shape).
		return Envelope{
			Kind:     "error-report",
			Title:    "Agent unavailable",
			Content:  c.Reason,
			Severity: "error",
		}
	}

	return Envelope{
		Kind:     "error-report",
		Title:    "Recovery error",
		Content:  "Internal recovery state error.",
		Severity: "error",
	}
}

// newCancelToken generates an opaque random token for [Cancel retry]
// info-cards. Stored in broker.activeRetries -> CancelFunc map; the FE
// echoes the token back in the cancel_retry event.
func newCancelToken() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		// Random read failure is exotic; degrade to a placeholder. The
		// FE button still works (cancel_retry by sessionID is the
		// fallback in Phase 5).
		return "cancel-degraded"
	}
	return hex.EncodeToString(b[:])
}
