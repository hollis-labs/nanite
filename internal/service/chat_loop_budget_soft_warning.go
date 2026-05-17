package service

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
)

// chatLoopBudgetSoftWarningEnvelopeType is the envelope `type` field emitted
// when the chat loop crosses the soft max_turns budget without terminating
// (CW-20260504-0001). Registered in config/envelopes.yaml and backed by
// internal/envelope/schemas/chat-loop-budget-soft-warning.schema.json.
const chatLoopBudgetSoftWarningEnvelopeType = "chat-loop-budget-soft-warning"

// chatLoopBudgetSoftWarningPayload is the data payload for the
// chat-loop-budget-soft-warning envelope. Field names match the schema.
type chatLoopBudgetSoftWarningPayload struct {
	MaxTurns  int    `json:"max_turns"`
	Iteration int    `json:"iteration"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// emitChatLoopBudgetSoftWarning logs a structured telemetry line at INFO
// (always — for inspector replay + production observability) and, when
// developer mode is enabled, sends a typed `chat-loop-budget-soft-warning`
// envelope on the session's stream so the FE can render an inline notice
// ("agent is exploring past the strategy planner's estimate"). The agent
// continues running — this is signal, not termination.
//
// Devmode gating mirrors the pattern in internal/api/skills.go:
//   - NANITE_DEVMODE=1 (env, truthy) → on
//   - user_settings.developer_mode = true → on
//   - otherwise → SSE silent, log + telemetry only
//
// One-shot: callers must use loopState.checkSoftMaxTurnsWarning to ensure
// the signal fires at most once per generation.
func (s *chatServiceImpl) emitChatLoopBudgetSoftWarning(
	sessionID string,
	iteration int,
	maxTurns int,
	ch chan chat.StreamEvent,
) {
	reason := "iteration crossed soft max_turns budget; agent continuing"
	slog.Info("chat-loop: soft max_turns budget crossed",
		"session_id", sessionID,
		"iteration", iteration,
		"max_turns", maxTurns,
		"reason", reason,
	)

	if !s.devModeEnabled() {
		return
	}

	payload := chatLoopBudgetSoftWarningPayload{
		MaxTurns:  maxTurns,
		Iteration: iteration,
		Reason:    reason,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("chat-service: marshal chat-loop-budget-soft-warning payload", "session", sessionID, "err", err)
		return
	}
	// DevModeOnly: this envelope's SSE emission is gated behind devModeEnabled()
	// above — it is dev-mode telemetry, not a real operator alert. The marker
	// rides at wrap level so the FE renders a "DEV" badge (CW-20260517-0008).
	streamWrap, err := buildPluginEnvelopeWrap("", chatLoopBudgetSoftWarningEnvelopeType, data, EnvelopeRouting{
		DisplayClass: EnvelopeDisplayClassAlert,
		DevModeOnly:  true,
	})
	if err != nil {
		slog.Warn("chat-service: marshal chat-loop-budget-soft-warning wrap", "session", sessionID, "err", err)
		return
	}
	ch <- chat.StreamEvent{
		Type:     "plugin_envelope",
		Envelope: string(streamWrap),
	}
}

// devModeEnabled reports whether developer-mode signals (e.g. the soft
// max_turns budget warning) should be surfaced on the SSE stream. Mirrors
// the gating in internal/api/skills.go: NANITE_DEVMODE env var first
// (process-level, no DB hit), then user_settings.developer_mode.
func (s *chatServiceImpl) devModeEnabled() bool {
	if envDevModeOn() {
		return true
	}
	if s.store == nil {
		return false
	}
	us, err := s.store.GetUserSettings()
	if err != nil || us == nil {
		return false
	}
	return us.DeveloperMode
}

// envDevModeOn reports whether NANITE_DEVMODE is set to a truthy value
// (1/true/yes/on). Local copy of the same predicate used by the API layer
// so the chat service does not import internal/api.
func envDevModeOn() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_DEVMODE")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
