package messaging

import (
	"testing"
	"time"

	gomessaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/nanite/internal/messaging/gomsg"
)

func TestToEnvelope_FromEnvelope_Roundtrip(t *testing.T) {
	readAt := "2026-05-18T12:05:00Z"
	orig := &Message{
		ID:            "msg-1",
		FromSessionID: "sess-a",
		FromAgentID:   "agent-a",
		ToSessionID:   "sess-b",
		ToAgentID:     "agent-b",
		ThreadID:      "thread-1",
		ReplyTo:       "msg-0",
		Type:          TypeHelpRequest,
		Subject:       "need a hand",
		Body:          "please review",
		Metadata:      `{"trace":"xyz"}`,
		Priority:      4,
		Status:        StatusRead,
		Channel:       ChannelInbox,
		Kind:          KindRequest,
		PayloadJSON:   `{"q":"why"}`,
		CreatedAt:     "2026-05-18T12:00:00Z",
		ReadAt:        &readAt,
	}

	env, err := ToEnvelope(orig, "nanite.local")
	if err != nil {
		t.Fatalf("ToEnvelope: %v", err)
	}
	if env.Kind != gomessaging.MsgKindRequest {
		t.Errorf("Kind = %q, want request", env.Kind)
	}
	if env.From.URN() != "msg://agent/nanite.local/sess-a/agent-a" {
		t.Errorf("From = %q", env.From.URN())
	}
	if env.To.URN() != "msg://agent/nanite.local/sess-b/agent-b" {
		t.Errorf("To = %q", env.To.URN())
	}
	if env.ConsumedAt == nil {
		t.Error("ConsumedAt nil — ReadAt should map to ConsumedAt")
	}

	back, err := FromEnvelope(env)
	if err != nil {
		t.Fatalf("FromEnvelope: %v", err)
	}

	checks := []struct {
		name      string
		got, want string
	}{
		{"FromSessionID", back.FromSessionID, orig.FromSessionID},
		{"FromAgentID", back.FromAgentID, orig.FromAgentID},
		{"ToSessionID", back.ToSessionID, orig.ToSessionID},
		{"ToAgentID", back.ToAgentID, orig.ToAgentID},
		{"ThreadID", back.ThreadID, orig.ThreadID},
		{"ReplyTo", back.ReplyTo, orig.ReplyTo},
		{"Type", back.Type, orig.Type},
		{"Subject", back.Subject, orig.Subject},
		{"Body", back.Body, orig.Body},
		{"Metadata", back.Metadata, orig.Metadata},
		{"Status", back.Status, orig.Status},
		{"Channel", back.Channel, orig.Channel},
		{"Kind", back.Kind, orig.Kind},
		{"PayloadJSON", back.PayloadJSON, orig.PayloadJSON},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s round-trip: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if back.Priority != orig.Priority {
		t.Errorf("Priority round-trip: got %d, want %d", back.Priority, orig.Priority)
	}
	if back.ReadAt == nil || *back.ReadAt != readAt {
		t.Errorf("ReadAt round-trip: got %v, want %q", back.ReadAt, readAt)
	}
	wantCreated, _ := time.Parse(time.RFC3339, orig.CreatedAt)
	if !env.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", env.CreatedAt, wantCreated)
	}
}

func TestToEnvelope_UserRecipient(t *testing.T) {
	m := &Message{
		ID:            "msg-2",
		FromSessionID: "s1",
		FromAgentID:   "agent-a",
		ToSessionID:   "s1",
		ToAgentID:     UserSentinel,
		Body:          "status: done",
		Channel:       ChannelChat,
		Kind:          KindNotification,
		CreatedAt:     "2026-05-18T12:00:00Z",
	}
	env, err := ToEnvelope(m, "nanite.local")
	if err != nil {
		t.Fatalf("ToEnvelope: %v", err)
	}
	if env.To.Kind != gomessaging.KindUser {
		t.Errorf("To.Kind = %q, want user", env.To.Kind)
	}
	if env.To.URN() != "msg://user/nanite.local/s1" {
		t.Errorf("To = %q", env.To.URN())
	}
	back, err := FromEnvelope(env)
	if err != nil {
		t.Fatalf("FromEnvelope: %v", err)
	}
	if back.ToAgentID != UserSentinel {
		t.Errorf("ToAgentID = %q, want %q", back.ToAgentID, UserSentinel)
	}
}

// TestFromEnvelope_ForeignEnvelope checks an envelope minted without the
// nanite.* metadata still folds in with sensible defaults.
func TestFromEnvelope_ForeignEnvelope(t *testing.T) {
	env := gomessaging.Envelope{
		Kind: gomessaging.MsgKindNotice,
		From: gomsg.AgentAddress("peer.example", "s1", "a"),
		To:   gomsg.AgentAddress("nanite.local", "s1", "b"),
	}
	m, err := FromEnvelope(env)
	if err != nil {
		t.Fatalf("FromEnvelope: %v", err)
	}
	if m.Type != TypeMessage || m.Priority != 2 || m.Status != StatusUnread {
		t.Errorf("defaults wrong: type=%q priority=%d status=%q", m.Type, m.Priority, m.Status)
	}
	if m.Channel != ChannelChat {
		t.Errorf("Channel = %q, want %q", m.Channel, ChannelChat)
	}
	if m.Kind != KindNotification {
		t.Errorf("Kind = %q, want %q", m.Kind, KindNotification)
	}
}
