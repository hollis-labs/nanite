package service

import (
	"context"
	"log/slog"
	"strings"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/subagent"
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
// transient losses remain unread without blocking later messages. A message
// larger than the empty slot can hold is Ack'd after one truncated delivery,
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
		if appendUserContext(slotResult, injection) {
			ack(m)
			continue
		}
		if slot.MaxTokens > 0 && len(injection) > slot.MaxTokens*4 {
			// Cannot fit even with an empty slot. Keep this truncated delivery
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
