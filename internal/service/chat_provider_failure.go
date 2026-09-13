package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// surfaceProviderFailure is called after the subagent-suppression decision.
// Persist the explanation with the partial answer before publishing the error,
// so a reload retains the same recovery choices as the live view.
func (s *chatServiceImpl) surfaceProviderFailure(ctx context.Context, ch chan<- chat.StreamEvent,
	sessionID, messageID, agentID, content, model, provider, profile string, err error, details map[string]interface{},
) {
	failure := chat.ProviderFailure(err, model, messageID)
	for key, value := range details {
		failure.Details[key] = value
	}
	metadata, _ := json.Marshal(map[string]interface{}{
		"had_error": true, "provider_error": failure, "partial_output": content != "",
	})
	text := content
	if text == "" {
		text = failure.Message
	}
	wrapped := chat.WrapResponse(text, "default", nil, nil, false, true)
	if saveErr := s.store.CreateMessage(context.WithoutCancel(ctx), &store.Message{
		ID: messageID, SessionID: sessionID, AgentID: agentID, Role: "assistant",
		Content: wrapped.MarshalContent(), Metadata: string(metadata),
	}); saveErr != nil {
		slog.Error("chat-service: failed to persist provider failure", "session_id", sessionID, "message_id", messageID, "err", saveErr)
	}
	ch <- chat.StreamEvent{Type: "error", Error: failure.Message, StructuredError: &failure}
	s.notifyRecoveryBrokerForHTTPStreamError(ctx, sessionID, provider, profile, err)
}
