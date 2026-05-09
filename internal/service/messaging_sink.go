package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/messaging"
)

// messagingStreamSink bridges messaging.Service's NotifyReceived hook
// to the session SSE stream. Every successful SendMessage produces a
// StreamEvent of type "message_received" on the target session's
// stream so any attached UI client (sidebar, notification chip) sees
// the incoming message in near-real-time.
//
// T7 scope: emit the event. Slot-pipeline integration (S3a) decides
// separately how the event flows into the agent's next envelope.
type messagingStreamSink struct {
	streams *StreamManager
}

// messageReceivedPayload is the JSON body of a message_received
// StreamEvent. Kept small — the UI fetches the full message via
// message_get on demand if it wants more than the summary.
type messageReceivedPayload struct {
	MessageID     string `json:"message_id"`
	Channel       string `json:"channel"`
	Kind          string `json:"kind"`
	FromSessionID string `json:"from_session_id"`
	FromAgentID   string `json:"from_agent_id"`
	ToSessionID   string `json:"to_session_id"`
	ToAgentID     string `json:"to_agent_id"`
	ThreadID      string `json:"thread_id"`
	Subject       string `json:"subject,omitempty"`
	Summary       string `json:"summary"`
}

// NotifyReceived packs the message into a StreamEvent and broadcasts
// it into the recipient's session stream. The broadcast drops
// silently if no SSE is currently attached (intended MVP behavior —
// the receiving agent will pick up the message from its inbox on
// next poll).
func (s *messagingStreamSink) NotifyReceived(_ context.Context, m *messaging.Message) {
	if s == nil || s.streams == nil || m == nil {
		return
	}
	payload := messageReceivedPayload{
		MessageID:     m.ID,
		Channel:       m.Channel,
		Kind:          m.Kind,
		FromSessionID: m.FromSessionID,
		FromAgentID:   m.FromAgentID,
		ToSessionID:   m.ToSessionID,
		ToAgentID:     m.ToAgentID,
		ThreadID:      m.ThreadID,
		Subject:       m.Subject,
		Summary:       summarize(m),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("messaging sink: marshal message_received payload", "err", err, "message_id", m.ID)
		return
	}
	evt := chat.StreamEvent{
		Type:      "message_received",
		MessageID: m.ID,
		AgentID:   m.ToAgentID,
		Envelope:  string(b),
	}
	_ = s.streams.BroadcastSessionStreamEvent(m.ToSessionID, evt)
}

// summarize generates a short preview of the message for the
// notification chip. Subject wins if present; otherwise the first
// 80 chars of the body.
func summarize(m *messaging.Message) string {
	if m.Subject != "" {
		return m.Subject
	}
	const maxLen = 80
	if len(m.Body) <= maxLen {
		return m.Body
	}
	return m.Body[:maxLen-1] + "…"
}
