package mailboxadapter

import (
	"context"
	"errors"
	"fmt"
	"testing"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := storetest.New(t, t.Context(), t.TempDir()+"/nanite.db")
	if err != nil {
		t.Fatalf("storetest.New: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st
}

func seedAgent(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.CreateAgent(t.Context(), &store.AgentProfile{
		ID: id, Slug: id, Name: id, Kind: "internal", Status: "active",
	}); err != nil {
		t.Fatalf("CreateAgent(%s): %v", id, err)
	}
}

func TestComponents_UsesMigratedSchemaAndPreservesMailboxLifecycle(t *testing.T) {
	st := newTestStore(t)
	seedAgent(t, st, "recipient")
	components := New(st)

	sent, err := components.Service.SendMessage(t.Context(), messaging.SendInput{
		FromSessionID: "session-1",
		FromAgentID:   a2a.UserSentinel,
		ToSessionID:   "session-1",
		ToAgentID:     "recipient",
		Channel:       messaging.ChannelInbox,
		Kind:          messaging.KindRequest,
		Body:          "please inspect",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	inbox, err := components.Service.Inbox(t.Context(), "session-1", "recipient",
		messaging.InboxFilter{Status: messaging.StatusUnread}, "session-1", "recipient")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != sent.ID || inbox[0].Body != "please inspect" {
		t.Fatalf("inbox = %+v, want sent message", inbox)
	}
	if _, inboxErr := components.Service.Inbox(t.Context(), "session-1", "recipient",
		messaging.InboxFilter{}, "session-1", "intruder"); !errors.Is(inboxErr, messaging.ErrForbidden) {
		t.Fatalf("spoofed Inbox error = %v, want ErrForbidden", inboxErr)
	}
	if ackErr := components.Service.Ack(t.Context(), "session-1", "recipient", sent.ID); ackErr != nil {
		t.Fatalf("Ack: %v", ackErr)
	}
	if resolveErr := components.Service.Resolve(t.Context(), "session-1", "recipient", sent.ID); resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	count, err := components.Service.UnreadCount(t.Context(), "session-1", "recipient")
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if count != 0 {
		t.Fatalf("UnreadCount = %d, want 0", count)
	}

	events, err := components.Service.SessionEvents(t.Context(), "session-1", 20)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	wantTypes := []string{
		messaging.EventMessageSent,
		messaging.EventMessageReceived,
		messaging.EventMessageAcked,
		messaging.EventMessageResolved,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("session events = %+v, want %d rows", events, len(wantTypes))
	}
	for index, want := range wantTypes {
		if events[index].EventType != want {
			t.Fatalf("event[%d].EventType = %q, want %q", index, events[index].EventType, want)
		}
	}
}

func TestComponents_ReadsMessageWrittenBeforeMailboxAdoption(t *testing.T) {
	st := newTestStore(t)
	seedAgent(t, st, "recipient")
	if _, err := st.DB.ExecContext(t.Context(), `
		INSERT INTO agent_messages (
			id, from_session_id, from_agent_id, to_session_id, to_agent_id,
			thread_id, body, status, channel, kind, payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "pre-adoption", "old-session", a2a.UserSentinel, "old-session", "recipient",
		"old-thread", "durable history", messaging.StatusUnread, messaging.ChannelChat,
		"subagent_result", `{}`, "2026-01-02T03:04:05Z"); err != nil {
		t.Fatalf("insert pre-adoption row: %v", err)
	}

	inbox, err := New(st).Service.Inbox(t.Context(), "old-session", "recipient",
		messaging.InboxFilter{Kind: "subagent_result"}, "old-session", "recipient")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != "pre-adoption" || inbox[0].Body != "durable history" {
		t.Fatalf("pre-adoption inbox = %+v", inbox)
	}
}

func TestComponents_AutoRegistrationRemainsNanitePolicy(t *testing.T) {
	st := newTestStore(t)
	seedAgent(t, st, "recipient")
	components := New(st)
	if _, err := components.Service.SendMessage(t.Context(), messaging.SendInput{
		FromSessionID: "session-1",
		FromAgentID:   "CLI.Host/One",
		ToSessionID:   "session-1",
		ToAgentID:     "recipient",
		RegisterAs:    "cli",
		Body:          "hello",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	profile, err := st.GetAgent(t.Context(), "CLI.Host/One")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if profile.Slug != "cli-host-one" || profile.Source != "auto" || profile.Kind != "cli" {
		t.Fatalf("auto-registered profile = %+v", profile)
	}
	if _, err := components.Service.SendMessage(t.Context(), messaging.SendInput{
		FromSessionID: "session-1", FromAgentID: a2a.UserSentinel,
		ToSessionID: "session-1", ToAgentID: "recipient", Body: "human follow-up",
	}); err != nil {
		t.Fatalf("SendMessage(user): %v", err)
	}
	if _, err := st.GetAgent(t.Context(), a2a.UserSentinel); err == nil {
		t.Fatal("reserved user participant unexpectedly has an agent_profiles row")
	}
}

func TestSessionEvents_RecentReturnsNewestPageInStableChronologicalOrder(t *testing.T) {
	st := newTestStore(t)
	components := New(st)
	for index := 1; index <= 5; index++ {
		if err := components.Events.Append(t.Context(), messaging.SessionEvent{
			ID: fmt.Sprintf("event-%d", index), SessionID: "session-1",
			EventType: "test", EnvelopePointerJSON: `{}`, CreatedAt: "2026-01-02T03:04:05Z",
		}); err != nil {
			t.Fatalf("Append(%d): %v", index, err)
		}
	}
	events, err := components.Service.SessionEvents(t.Context(), "session-1", 3)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	want := []string{"event-3", "event-4", "event-5"}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %v", events, want)
	}
	for index := range want {
		if events[index].ID != want[index] {
			t.Fatalf("event[%d].ID = %q, want %q", index, events[index].ID, want[index])
		}
	}
}

func TestSessionEvents_WritesNaniteRuntimeVocabularyBesideMailboxEvents(t *testing.T) {
	st := newTestStore(t)
	components := New(st)
	components.Events.WriteSessionEvent(t.Context(), "session-1", "pty_turn_start", "pty", `{"provider":"codex"}`)
	events, err := components.Service.SessionEvents(t.Context(), "session-1", 10)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "pty_turn_start" || events[0].Channel != "pty" || events[0].EnvelopePointerJSON != `{"provider":"codex"}` {
		t.Fatalf("runtime event = %+v", events)
	}
}

func TestComponents_HandoffUsesNaniteTransactionAndPolicy(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		seedAgent(t, st, id)
	}
	session := &store.Session{}
	if err := st.CreateSession(t.Context(), session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.EnsureSessionAgent(t.Context(), session.ID, "agent-a", "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}
	service := New(st).Service
	if _, err := service.RequestHandoff(t.Context(), session.ID, "agent-a", "agent-b", "robot"); !errors.Is(err, messaging.ErrValidation) {
		t.Fatalf("invalid requested_by error = %v, want ErrValidation", err)
	}
	first, err := service.RequestHandoff(t.Context(), session.ID, "agent-a", "agent-b", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff(first): %v", err)
	}
	second, err := service.RequestHandoff(t.Context(), session.ID, "agent-a", "agent-c", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff(second): %v", err)
	}
	if approveErr := service.ApproveHandoff(t.Context(), first); approveErr != nil {
		t.Fatalf("ApproveHandoff: %v", approveErr)
	}
	if approveErr := service.ApproveHandoff(t.Context(), first); approveErr != nil {
		t.Fatalf("ApproveHandoff idempotent retry: %v", approveErr)
	}
	agents, err := st.ListSessionAgents(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("ListSessionAgents: %v", err)
	}
	primaryCount := 0
	for _, agent := range agents {
		if agent.IsPrimary {
			primaryCount++
			if agent.AgentID != "agent-b" {
				t.Fatalf("primary agent = %q, want agent-b", agent.AgentID)
			}
		}
	}
	if primaryCount != 1 {
		t.Fatalf("primary count = %d, want 1", primaryCount)
	}
	var status string
	if err := st.DB.QueryRowContext(t.Context(), `SELECT status FROM session_handoffs WHERE id = ?`, second).Scan(&status); err != nil {
		t.Fatalf("read superseded handoff: %v", err)
	}
	if status != "rejected" {
		t.Fatalf("superseded handoff status = %q, want rejected", status)
	}
	if err := service.ApproveHandoff(t.Context(), "missing"); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("missing handoff error = %v, want ErrNotFound", err)
	}
}
