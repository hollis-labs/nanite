package recovery

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// Envelope kinds the broker emits. Reused from the existing chat
// envelope schemas — no new kinds. info-card / error-report /
// chat-loop-terminated render through the existing FE projection.
const (
	envelopeKindInfoCard            = "info-card"
	envelopeKindErrorReport         = "error-report"
	envelopeKindChatLoopTerminated  = "chat-loop-terminated"
)

// Severity levels used on the broker's envelopes. Match the existing
// chat surface vocabulary.
const (
	severityInfo    = "info"
	severityWarning = "warning"
	severityError   = "error"
)

// RenderUserMessage builds the envelope the broker emits for a given
// (FailureEvent, Classification, Action) triple. Routing:
//
//	ActionRetryTransient    -> info-card "Reconnecting agent"
//	ActionRetryConfigFixed  -> info-card "Fixed an agent issue" (with Remediation.Description)
//	ActionPermanentFailure  -> error-report "Agent unavailable"
//	  (or chat-loop-terminated when Cause == restart_exhausted —
//	   the failure is runaway-shaped and matches the existing
//	   chat-loop-terminated schema's "supervisor exhausted" semantics)
//
// info-card kinds carry a CancelToken so the FE [Cancel retry] button
// can route the cancel_retry event back to broker.Cancel (which
// validates the token's session binding).
func (b *Broker) RenderUserMessage(ev *FailureEvent, c Classification, action Action) Envelope {
	switch action {
	case ActionRetryTransient:
		return Envelope{
			Kind:        envelopeKindInfoCard,
			Title:       "Reconnecting agent",
			Content:     transientRetryMessage(ev, c),
			CancelToken: newCancelToken(),
			Severity:    severityInfo,
		}

	case ActionRetryConfigFixed:
		return Envelope{
			Kind:        envelopeKindInfoCard,
			Title:       "Fixed an agent issue",
			Content:     configFixedMessage(c),
			CancelToken: newCancelToken(),
			Severity:    severityInfo,
		}

	case ActionPermanentFailure:
		return permanentEnvelope(ev, c)

	default:
		return Envelope{
			Kind:     envelopeKindErrorReport,
			Title:    "Recovery error",
			Content:  "Internal recovery state error — broker reached an unknown action.",
			Severity: severityError,
		}
	}
}

// transientRetryMessage renders the body for ActionRetryTransient. The
// classifier's Reason gives context-appropriate flavor without leaking
// stderr (which may carry user-private content).
func transientRetryMessage(ev *FailureEvent, c Classification) string {
	cause := ""
	if ev != nil && ev.Exit != nil {
		cause = ev.Exit.Cause
	}
	switch cause {
	case agentsessions.CauseIdleTimeout:
		return "The agent went idle and was reset. Reconnecting now…"
	case agentsessions.CauseOOMKill:
		return "The agent ran out of memory and was restarted. Reconnecting…"
	}
	// Default: brief generic message. The classifier's Reason is for
	// telemetry/postmortem, not the chat surface.
	_ = c
	return "Agent ran into a temporary error. Retrying now…"
}

// configFixedMessage renders the body for ActionRetryConfigFixed. The
// Remediation.Description field provides the user-friendly phrase
// describing what was fixed.
func configFixedMessage(c Classification) string {
	return fmt.Sprintf("Resolved %s; reconnecting agent…", c.Remediation.Description())
}

// permanentEnvelope chooses between error-report and chat-loop-terminated
// based on the failure's shape. chat-loop-terminated maps onto the
// existing schema's "the agent process gave up" semantics — used when
// CauseRestartExhausted lands (lib-level retries exhausted, broker
// hard cap exhausted) since that's the closest existing schema.
//
// Everything else uses error-report, which is the general-purpose
// "operation failed" envelope.
func permanentEnvelope(ev *FailureEvent, c Classification) Envelope {
	cause := ""
	if ev != nil && ev.Exit != nil {
		cause = ev.Exit.Cause
	}
	if cause == agentsessions.CauseRestartExhausted {
		return Envelope{
			Kind:     envelopeKindChatLoopTerminated,
			Title:    "Agent stopped",
			Content:  "The agent kept failing after multiple restart attempts. " + permanentSuggestedAction(ev, c),
			Severity: severityError,
		}
	}
	return Envelope{
		Kind:     envelopeKindErrorReport,
		Title:    "Agent unavailable",
		Content:  permanentDetail(ev, c) + " " + permanentSuggestedAction(ev, c),
		Severity: severityError,
	}
}

// permanentDetail produces the failure detail for error-report bodies.
// Reason carries the classifier's human-readable explanation; we don't
// leak ExitError.Code/Signal/etc. to the chat surface (postmortem
// breadcrumb has those).
func permanentDetail(_ *FailureEvent, c Classification) string {
	if c.Reason == "" {
		return "The agent could not be recovered."
	}
	// Classifier reasons are shaped for logs (e.g. "code 127: agent binary not found on PATH").
	// Render as a single trimmed sentence.
	r := strings.TrimSpace(c.Reason)
	if !strings.HasSuffix(r, ".") {
		r += "."
	}
	return capitalize(r)
}

// permanentSuggestedAction returns a short recommendation appended to
// permanent-failure messages. Stays generic; specific provider-fallback
// suggestions are out of scope for v1 (locked).
func permanentSuggestedAction(_ *FailureEvent, c Classification) string {
	switch c.Remediation {
	case RemediationRefreshCredentials:
		return "Check your API credentials and try again."
	case RemediationRepopulateSandbox, RemediationRegenerateCLAUDEMD:
		return "Try restarting the chat session."
	default:
		return "Try a different agent or restart the session."
	}
}

// capitalize uppercases the first rune of s. Tiny helper to clean the
// classifier's lower-case reason text for surface presentation.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
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
