package service

import (
	"context"
	"log/slog"
	"strings"

	ctxpkg "github.com/hollis-labs/substrate/agent/contextwindow"
	"github.com/hollis-labs/substrate/agent/subagent"
	messaging "github.com/hollis-labs/substrate/mesh/messaging/mailbox"
)

// evaluateAndInjectSubagentResults surfaces pending kind=subagent_result
// agent_messages for (sessionID, agentID) into SlotUserContext
// (CW-20260512-0019). Mirrors evaluateAndInjectReflexes's shape: nil-safe
// via the subagentInbox guard, injects via the same appendUserContext
// helper reflexes and reminders use, and returns what was injected so the
// caller can decide whether to refresh its local systemPrompt copy.
//
// Without always-ship owners, preserve the historic batch and unconditional Ack.
// With an owner, deliver messages separately: exact surviving text is Ack'd;
// losses that fit the available byte headroom remain unread. A message
// larger than the currently free headroom is Ack'd after one truncated delivery,
// retaining the historic oversized-result behavior rather than wedging the inbox.
func (s *chatServiceImpl) evaluateAndInjectSubagentResults(ctx context.Context, sessionID, agentID string, slotResult *SlotAssemblyResult) []messaging.Message {
	if s.subagentInbox == nil || slotResult == nil || slotResult.Window == nil {
		return nil
	}
	pending, err := s.subagentInbox.Inbox(ctx, sessionID, agentID,
		messaging.InboxFilter{Status: messaging.StatusUnread, Kind: subagent.ResultMessageKind},
		sessionID, agentID)
	if err != nil {
		slog.Warn("chat-service: subagent result inbox query failed", "session_id", sessionID, "err", err)
		return nil
	}
	if len(pending) == 0 {
		return nil
	}

	ack := func(m messaging.Message) {
		if ackErr := s.subagentInbox.Ack(ctx, sessionID, agentID, m.ID); ackErr != nil {
			slog.Warn("chat-service: ack subagent result message failed", "session_id", sessionID, "message_id", m.ID, "err", ackErr)
		}
	}
	if !slotResult.AlwaysShipActive {
		appendUserContext(slotResult, formatSubagentResultInjection(pending))
		for _, m := range pending {
			ack(m)
		}
		return pending
	}
	for _, m := range pending {
		injection := formatSubagentResultInjection([]messaging.Message{m})
		slot := slotResult.Window.Slot(ctxpkg.SlotUserContext)
		before := slot.Content
		freeBytes := subagentResultHeadroomBytes(slot)
		if appendUserContext(slotResult, injection) {
			ack(m)
			continue
		}
		if slot.MaxTokens > 0 && len(injection) > freeBytes {
			// Cannot fit the currently free space. Keep this truncated delivery
			// and Ack once, allowing queued results through on later turns.
			slog.Warn("chat-service: oversized subagent result delivered truncated", "session_id", sessionID, "message_id", m.ID)
			ack(m)
			continue
		}
		// Restore the failed append so smaller later messages can use the
		// remaining space; an old duplicate does not prove this delivery.
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, before)
		slotResult.Blocks = slotResult.Window.Assemble()
		slotResult.SystemPrompt = rebuildLegacySystemPrompt(slotResult.Window)
		slog.Warn("chat-service: subagent result truncated; leaving message unread", "session_id", sessionID, "message_id", m.ID)
	}
	return pending
}

// Match Window's existing byte clamp, counting appendUserContext's separator.
// Compute before the append: Assemble mutates the slot on truncation.
func subagentResultHeadroomBytes(slot *ctxpkg.Slot) int {
	separator := 0
	if slot.Content != "" {
		separator = 2
	}
	return max(0, slot.MaxTokens*4-len(slot.Content)-separator)
}

func formatSubagentResultInjection(messages []messaging.Message) string {
	if len(messages) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<system-reminder>\n")
	for _, message := range messages {
		body := strings.TrimSpace(message.Body)
		if body == "" {
			body = "(no summary)"
		}
		builder.WriteString("Subagent result (from ")
		builder.WriteString(message.FromAgentID)
		builder.WriteString("): ")
		builder.WriteString(body)
		builder.WriteString("\n")
	}
	builder.WriteString("</system-reminder>")
	return builder.String()
}
