package gomsg_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	messaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/messagingtest"
	"github.com/hollis-labs/nanite/internal/messaging/gomsg"
	"github.com/hollis-labs/nanite/internal/store"
)

// newContractStore builds a fresh SQLStore on a temp SQLite DB. Migration
// 064 (run by store.New) provides the messaging_envelopes table.
func newContractStore(t *testing.T) messaging.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gomsg.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return gomsg.NewSQLStore(s.DB)
}

// TestSQLStore_Contract runs the full go-messaging Store contract suite
// against the SQLite-backed store — proving Nanite's durable store is a
// drop-in conforming peer alongside the reference impl.
func TestSQLStore_Contract(t *testing.T) {
	messagingtest.RunContract(t, newContractStore)
}

// TestSQLStore_MetadataRoundTrip covers Metadata + Payload persistence,
// which the contract suite does not exercise.
func TestSQLStore_MetadataRoundTrip(t *testing.T) {
	s := newContractStore(t)
	ctx := context.Background()

	sent, err := s.Send(ctx, messaging.Envelope{
		Kind:        messaging.MsgKindNotice,
		Channel:     "inbox",
		From:        gomsg.AgentAddress("nanite.local", "sess-1", "agent-a"),
		To:          gomsg.AgentAddress("nanite.local", "sess-1", "agent-b"),
		Payload:     json.RawMessage(`{"text":"hi"}`),
		ContentType: "application/json",
		Metadata:    map[string]string{"k1": "v1", "k2": "v2"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	got, err := s.Get(ctx, sent.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Channel != "inbox" {
		t.Errorf("Channel = %q, want inbox", got.Channel)
	}
	if string(got.Payload) != `{"text":"hi"}` {
		t.Errorf("Payload = %s", got.Payload)
	}
	if got.Metadata["k1"] != "v1" || got.Metadata["k2"] != "v2" {
		t.Errorf("Metadata = %v", got.Metadata)
	}
}

// TestSQLStore_ChannelFilter checks Filter.Channel narrowing on Inbox.
func TestSQLStore_ChannelFilter(t *testing.T) {
	s := newContractStore(t)
	ctx := context.Background()
	to := gomsg.AgentAddress("nanite.local", "sess-1", "agent-b")

	base := messaging.Envelope{
		Kind: messaging.MsgKindNotice,
		From: gomsg.AgentAddress("nanite.local", "sess-1", "agent-a"),
		To:   to,
	}
	chat := base
	chat.Channel = "chat"
	alert := base
	alert.Channel = "alert"
	if _, err := s.Send(ctx, chat); err != nil {
		t.Fatalf("Send chat: %v", err)
	}
	if _, err := s.Send(ctx, alert); err != nil {
		t.Fatalf("Send alert: %v", err)
	}

	got, err := s.Inbox(ctx, to, messaging.Filter{Channel: []messaging.Channel{"alert"}})
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(got) != 1 || got[0].Channel != "alert" {
		t.Fatalf("Channel filter: got %d envelopes %+v", len(got), got)
	}
}
