package service

import (
	"github.com/hollis-labs/nanite/internal/chat"
)

// subagentStreamSink bridges subagent.Service's status emission to the
// parent session's SSE stream. Each transition (running, completed,
// failed, canceled) becomes a chat.StreamEvent of type
// "subagent_run_status_changed" on the parent session.
//
// Mirrors the T7 messagingStreamSink pattern (messaging_sink.go).
type subagentStreamSink struct {
	streams *StreamManager
}

// SubagentStatusChanged broadcasts the run-status payload into the
// parent session's stream. Drops silently if no SSE is attached
// (intended behavior — no consumer means nothing to update).
//
// The JSON payload is placed in StreamEvent.Envelope to match the
// convention used by sibling session-level events: message_received
// (messaging_sink.go:66), plugin_envelope (stream.go:394), stream_end
// (chat_generate.go:837). StreamEvent.Data is reserved for transient
// UI payloads (tool_warning, approval_request).
func (s *subagentStreamSink) SubagentStatusChanged(parentSessionID string, payload []byte) {
	if s == nil || s.streams == nil || parentSessionID == "" {
		return
	}
	_ = s.streams.BroadcastSessionStreamEvent(parentSessionID, chat.StreamEvent{
		Type:     "subagent_run_status_changed",
		Envelope: string(payload),
	})
}
