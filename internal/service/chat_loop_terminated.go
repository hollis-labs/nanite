package service

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
)

// chatLoopTerminatedEnvelopeType is the envelope `type` field emitted when
// the chat loop exits abnormally. Registered in config/envelopes.yaml and
// backed by internal/envelope/schemas/chat-loop-terminated.schema.json.
// CW-20260417-0485.
const chatLoopTerminatedEnvelopeType = "chat-loop-terminated"

// chatLoopTerminatedPayload is the data payload for the chat-loop-terminated
// envelope. Field names match internal/envelope/schemas/chat-loop-terminated.schema.json.
type chatLoopTerminatedPayload struct {
	Reason              string `json:"reason"`
	Code                string `json:"code"`
	Iteration           int    `json:"iteration"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastError           string `json:"last_error,omitempty"`
	LastTool            string `json:"last_tool,omitempty"`
	Timestamp           string `json:"timestamp"`
}

// emitChatLoopTerminated sends a typed `chat-loop-terminated` envelope on the
// session's stream so the FE can render a terminal pause card. The envelope
// is shaped like other plugin_envelope events (see envelope_emit.go) — wrapped
// in `{id, type, data}` — so EnvelopeRenderer routes to the correct component
// via `type`. CW-20260417-0485.
//
// We intentionally do not persist the envelope through store.CreateEnvelopeInstance:
// chat-loop-terminated is a stream-only signal — it describes a transient
// termination event, not a user-interactable card that needs a DB row for
// /api/envelopes/:id/respond. Keeping it stream-only avoids churning the
// envelope_instances table on every terminal pause.
func (s *chatServiceImpl) emitChatLoopTerminated(
	sessionID string,
	ls *loopState,
	code TerminationCode,
	reason string,
	ch chan chat.StreamEvent,
) {
	payload := chatLoopTerminatedPayload{
		Reason:              reason,
		Code:                string(code),
		Iteration:           ls.iteration,
		ConsecutiveFailures: ls.consecutiveFailures,
		LastError:           ls.lastToolError,
		LastTool:            ls.lastToolName,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("chat-service: marshal chat-loop-terminated payload", "session", sessionID, "err", err)
		return
	}
	streamWrap, err := buildPluginEnvelopeWrap("", chatLoopTerminatedEnvelopeType, data, EnvelopeRouting{
		DisplayClass: EnvelopeDisplayClassAlert,
	})
	if err != nil {
		slog.Warn("chat-service: marshal chat-loop-terminated wrap", "session", sessionID, "err", err)
		return
	}
	ch <- chat.StreamEvent{
		Type:     "plugin_envelope",
		Envelope: string(streamWrap),
	}
}
