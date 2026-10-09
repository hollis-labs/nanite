package agent

import (
	"strings"

	"github.com/hollis-labs/substrate/harness/adapters/provider"
)

// Turn failures go-providers classifies (CW-20260930-0113). Since
// go-agent-wrapper v0.15.0 hands agentkit the go-providers adapter
// unwrapped, its SessionLostClassifier and AuthFailureClassifier take
// effect: a resume the provider no longer has fails with
// provider.ErrProviderSessionLost, and a CLI that is not logged in with
// provider.ErrProviderNotAuthenticated. Those reach Nanite's readers as the
// turn's error text (turn.failed's "error", or SendInput's error), so they
// are recognized by the sentinels' messages.

const (
	// TurnFailureSessionLost: the provider no longer has the conversation
	// the turn tried to resume.
	TurnFailureSessionLost = "session_lost"
	// TurnFailureNotAuthenticated: the runtime's CLI is not logged in on
	// this host.
	TurnFailureNotAuthenticated = "not_authenticated"
)

// ClassifyTurnFailure returns TurnFailureSessionLost,
// TurnFailureNotAuthenticated, or "" for a failed turn's error text.
func ClassifyTurnFailure(msg string) string {
	switch {
	case strings.Contains(msg, provider.ErrProviderSessionLost.Error()):
		return TurnFailureSessionLost
	case strings.Contains(msg, provider.ErrProviderNotAuthenticated.Error()):
		return TurnFailureNotAuthenticated
	default:
		return ""
	}
}

// UserFacingTurnError appends a plain-language next step to a classified
// failure's error text, which is what the chat stream shows. Other errors
// pass through unchanged.
func UserFacingTurnError(msg string) string {
	switch ClassifyTurnFailure(msg) {
	case TurnFailureSessionLost:
		return msg + " (the provider no longer has this conversation; your next message starts a fresh one)"
	case TurnFailureNotAuthenticated:
		return msg + " (the agent's CLI is not logged in on this host; log it in, then retry)"
	default:
		return msg
	}
}
