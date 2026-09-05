package selftools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/store/mailboxadapter"
)

func messagingToolText(t *testing.T, st *SelfToolsTransport, ctx context.Context, name string, args map[string]any) string {
	t.Helper()
	result, err := st.CallTool(ctx, name, args)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool(%s) returned error: %#v", name, result.Content)
	}
	if len(result.Content) != 1 {
		t.Fatalf("CallTool(%s) content len = %d, want 1", name, len(result.Content))
	}
	return result.Content[0].Text
}

func createMessagingSession(t *testing.T, s *store.Store) string {
	t.Helper()
	sess := &store.Session{}
	if err := s.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess.ID
}

func TestSelfToolsTransport_MessagingCharacterization(t *testing.T) {
	t.Run("normal send inbox thread ack resolve and catch-up default", func(t *testing.T) {
		st := newSelfTools(t)
		st.MessagingTools.Service = newTestMessaging(t)

		const (
			fromSession = "sess-from"
			toSession   = "sess-to"
		)
		sendText := messagingToolText(t, st, t.Context(), "message_send", map[string]any{
			"from_session_id": fromSession,
			"from_agent_id":   a2a.UserSentinel,
			"to_session_id":   toSession,
			"to_agent_id":     a2a.UserSentinel,
			"body":            "characterize transport",
		})
		messageID := strings.TrimPrefix(sendText, "sent: ")
		if messageID == sendText || messageID == "" {
			t.Fatalf("message_send result = %q, want sent id", sendText)
		}

		inboxText := messagingToolText(t, st, t.Context(), "message_inbox", map[string]any{
			"session_id": toSession,
			"agent_id":   a2a.UserSentinel,
		})
		var inbox []messaging.Message
		if err := json.Unmarshal([]byte(inboxText), &inbox); err != nil {
			t.Fatalf("decode inbox: %v", err)
		}
		if len(inbox) != 1 || inbox[0].ID != messageID {
			t.Fatalf("inbox = %#v, want message %q", inbox, messageID)
		}

		threadText := messagingToolText(t, st, t.Context(), "message_thread", map[string]any{
			"thread_id":  inbox[0].ThreadID,
			"session_id": toSession,
			"agent_id":   a2a.UserSentinel,
		})
		var thread []messaging.Message
		if err := json.Unmarshal([]byte(threadText), &thread); err != nil {
			t.Fatalf("decode thread: %v", err)
		}
		if len(thread) != 1 || thread[0].ID != messageID {
			t.Fatalf("thread = %#v, want message %q", thread, messageID)
		}

		if got := messagingToolText(t, st, t.Context(), "message_ack", map[string]any{
			"session_id": toSession,
			"agent_id":   a2a.UserSentinel,
			"message_id": messageID,
		}); got != "acked" {
			t.Fatalf("message_ack = %q, want acked", got)
		}
		if got := messagingToolText(t, st, t.Context(), "message_resolve", map[string]any{
			"session_id": toSession,
			"agent_id":   a2a.UserSentinel,
			"message_id": messageID,
		}); got != "resolved" {
			t.Fatalf("message_resolve = %q, want resolved", got)
		}

		for i := 0; i < 25; i++ {
			messagingToolText(t, st, t.Context(), "message_send", map[string]any{
				"from_session_id": fromSession,
				"from_agent_id":   a2a.UserSentinel,
				"to_session_id":   toSession,
				"to_agent_id":     a2a.UserSentinel,
				"body":            "catch-up",
			})
		}
		catchUpText := messagingToolText(t, st, t.Context(), "message_catch_up", map[string]any{
			"session_id": toSession,
		})
		var catchUp []messaging.Message
		if err := json.Unmarshal([]byte(catchUpText), &catchUp); err != nil {
			t.Fatalf("decode catch-up: %v", err)
		}
		if len(catchUp) != 20 {
			t.Fatalf("default catch-up len = %d, want 20", len(catchUp))
		}
	})

	t.Run("nil inbox and thread serialize as arrays", func(t *testing.T) {
		st := newSelfTools(t)
		st.MessagingTools.Service = newTestMessaging(t)
		if got := messagingToolText(t, st, t.Context(), "message_inbox", map[string]any{
			"session_id": "empty-session",
			"agent_id":   a2a.UserSentinel,
		}); got != "[]" {
			t.Fatalf("empty inbox = %q, want []", got)
		}
		if got := messagingToolText(t, st, t.Context(), "message_thread", map[string]any{
			"thread_id":  "empty-thread",
			"session_id": "empty-session",
			"agent_id":   a2a.UserSentinel,
		}); got != "[]" {
			t.Fatalf("empty thread = %q, want []", got)
		}
	})

	t.Run("handoff uses context session and supports approve reject", func(t *testing.T) {
		st := newSelfTools(t)
		st.MessagingTools.Service = mailboxadapter.New(st.Store).Service
		fromAgent := seedAgent(t, st.Store, "From Agent", "handoff-from", "", "")
		toAgent := seedAgent(t, st.Store, "To Agent", "handoff-to", "", "")
		sessionID := createMessagingSession(t, st.Store)
		if err := st.Store.EnsureSessionAgent(t.Context(), sessionID, fromAgent.ID, "default", true); err != nil {
			t.Fatalf("EnsureSessionAgent: %v", err)
		}
		ctx := mcp.WithSessionID(t.Context(), sessionID)

		requestText := messagingToolText(t, st, ctx, "handoff_request", map[string]any{
			"session_id":    "spoofed-session",
			"from_agent_id": fromAgent.ID,
			"to_agent_id":   toAgent.ID,
			"requested_by":  "user",
		})
		handoffID := strings.TrimPrefix(requestText, "handoff requested: ")
		if handoffID == requestText || handoffID == "" {
			t.Fatalf("handoff_request = %q, want handoff id", requestText)
		}
		if got := messagingToolText(t, st, ctx, "handoff_approve", map[string]any{"handoff_id": handoffID}); got != "approved" {
			t.Fatalf("handoff_approve = %q, want approved", got)
		}
		primary, err := st.Store.GetSessionPrimaryAgent(t.Context(), sessionID)
		if err != nil {
			t.Fatalf("GetSessionPrimaryAgent: %v", err)
		}
		if primary.AgentID != toAgent.ID {
			t.Fatalf("primary agent = %q, want %q", primary.AgentID, toAgent.ID)
		}

		rejectSessionID := createMessagingSession(t, st.Store)
		if err := st.Store.EnsureSessionAgent(t.Context(), rejectSessionID, fromAgent.ID, "default", true); err != nil {
			t.Fatalf("EnsureSessionAgent reject session: %v", err)
		}
		rejectCtx := mcp.WithSessionID(t.Context(), rejectSessionID)
		rejectRequest := messagingToolText(t, st, rejectCtx, "handoff_request", map[string]any{
			"from_agent_id": fromAgent.ID,
			"to_agent_id":   toAgent.ID,
			"requested_by":  "user",
		})
		rejectID := strings.TrimPrefix(rejectRequest, "handoff requested: ")
		if got := messagingToolText(t, st, rejectCtx, "handoff_reject", map[string]any{
			"handoff_id": rejectID,
			"reason":     "not now",
		}); got != "rejected" {
			t.Fatalf("handoff_reject = %q, want rejected", got)
		}
	})

	t.Run("unwired service returns existing error for every name", func(t *testing.T) {
		st := newSelfTools(t)
		for _, name := range []string{
			"message_send", "message_inbox", "message_thread", "message_ack",
			"message_resolve", "message_catch_up", "handoff_request",
			"handoff_approve", "handoff_reject",
		} {
			t.Run(name, func(t *testing.T) {
				result, err := st.CallTool(t.Context(), name, map[string]any{})
				if err != nil {
					t.Fatalf("CallTool: %v", err)
				}
				if !result.IsError || result.Content[0].Text != "messaging service not configured" {
					t.Fatalf("result = %#v, want existing unwired error", result)
				}
			})
		}
	})
}
