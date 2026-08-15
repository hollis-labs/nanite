package service

import (
	"context"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/messaging"
)

// evaluateAndInjectSubagentResults surfaces pending kind=subagent_result
// agent_messages for (sessionID, agentID) into SlotUserContext
// (CW-20260512-0019). Mirrors evaluateAndInjectReflexes's shape: nil-safe
// via the subagentInbox guard, injects via the same appendUserContext
// helper reflexes and reminders use, and returns what was injected so the
// caller can decide whether to refresh its local systemPrompt copy.
//
// Messages are Ack'd immediately after formatting so a later turn never
// re-injects them — the sole idempotency guarantee this needs, since
// nothing else marks kind=subagent_result rows read. An Ack failure is
// logged but not fatal: worst case a message is re-surfaced next turn,
// which is a duplicate nudge, not a correctness bug.
func (s *chatServiceImpl) evaluateAndInjectSubagentResults(ctx context.Context, sessionID, agentID string, slotResult *SlotAssemblyResult) []messaging.Message {
	if s.subagentInbox == nil || slotResult == nil || slotResult.Window == nil {
		return nil
	}
	pending, err := s.subagentInbox.Inbox(ctx, sessionID, agentID,
		messaging.InboxFilter{Status: messaging.StatusUnread, Kind: messaging.KindSubagentResult},
		sessionID, agentID)
	if err != nil {
		slog.Warn("chat-service: subagent result inbox query failed", "session_id", sessionID, "err", err)
		return nil
	}
	if len(pending) == 0 {
		return nil
	}

	injection := messaging.FormatSubagentResultInjection(pending)
	appendUserContext(slotResult, injection)

	for _, m := range pending {
		if ackErr := s.subagentInbox.Ack(ctx, sessionID, agentID, m.ID); ackErr != nil {
			slog.Warn("chat-service: ack subagent result message failed", "session_id", sessionID, "message_id", m.ID, "err", ackErr)
		}
	}
	return pending
}
