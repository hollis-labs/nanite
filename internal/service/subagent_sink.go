package service

import (
	"github.com/hollis-labs/nanite/internal/chat"
)

// subagentStreamSink bridges subagent.Service's status emission to the
// parent session's SSE stream. Each transition (running, completed,
// failed, cancelled) becomes a chat.StreamEvent of type
// "subagent_run_status_changed" on the parent session.
//
// Mirrors the T7 messagingStreamSink pattern (messaging_sink.go).
type subagentStreamSink struct {
	streams *StreamManager
}

// SubagentStatusChanged broadcasts the run-status payload into the
// parent session's stream. Drops silently if no SSE is attached
// (intended behavior — no consumer means nothing to update).
func (s *subagentStreamSink) SubagentStatusChanged(parentSessionID string, payload []byte) {
	if s == nil || s.streams == nil || parentSessionID == "" {
		return
	}
	_ = s.streams.BroadcastSessionStreamEvent(parentSessionID, chat.StreamEvent{
		Type: "subagent_run_status_changed",
		Data: string(payload),
	})
}
