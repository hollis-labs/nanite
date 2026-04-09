package a2a

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// newTestA2AStore spins up a fresh file-backed SQLite store in a temp dir
// and runs all embedded migrations. A file-backed DB (rather than :memory:)
// avoids any surprise around shared-cache semantics on modernc.org/sqlite.
func newTestA2AStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newTestService wires up a Service backed by a fresh store and a fakeResolver
// that knows the supplied agent IDs. Returns both so tests can seed rows
// directly through the store when they want to bypass validation.
func newTestService(t *testing.T, knownAgents ...string) (*Service, *store.Store) {
	t.Helper()
	s := newTestA2AStore(t)
	r := newFakeResolver(knownAgents...)
	return NewService(s, r), s
}

// baseMessage builds a minimal valid A2AMessage for tests that only care
// about the validation or routing behavior.
func baseMessage(from, to string) *store.A2AMessage {
	return &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   from,
		ToSessionID:   "sess-1",
		ToAgentID:     to,
		Body:          "hello",
	}
}

func TestService_SendMessage_RejectsUnknownTo(t *testing.T) {
	svc, _ := newTestService(t, "file-backend")
	msg := baseMessage("file-backend", "file-nonexistent")

	_, err := svc.SendMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown to_agent_id, got nil")
	}
	if !strings.Contains(err.Error(), "to_agent_id") {
		t.Errorf("expected error to mention to_agent_id, got: %v", err)
	}
}

func TestService_SendMessage_RejectsUnknownFrom(t *testing.T) {
	svc, _ := newTestService(t, "file-backend")
	msg := baseMessage("file-nonexistent", "file-backend")

	_, err := svc.SendMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown from_agent_id, got nil")
	}
	if !strings.Contains(err.Error(), "from_agent_id") {
		t.Errorf("expected error to mention from_agent_id, got: %v", err)
	}
}

func TestService_SendMessage_AcceptsUserSentinel(t *testing.T) {
	// Resolver knows file-backend. The user sentinel short-circuits validation,
	// so it must work on either side of the send.
	svc, _ := newTestService(t, "file-backend")

	// user -> file-backend
	msg1 := baseMessage(UserSentinel, "file-backend")
	out, err := svc.SendMessage(context.Background(), msg1)
	if err != nil {
		t.Fatalf("user -> file-backend: unexpected error: %v", err)
	}
	if out == nil || out.ID == "" {
		t.Fatal("expected persisted message with non-empty ID")
	}
	if out.FromAgentID != UserSentinel || out.ToAgentID != "file-backend" {
		t.Errorf("addressing not preserved: from=%q to=%q", out.FromAgentID, out.ToAgentID)
	}

	// file-backend -> user
	msg2 := baseMessage("file-backend", UserSentinel)
	out2, err := svc.SendMessage(context.Background(), msg2)
	if err != nil {
		t.Fatalf("file-backend -> user: unexpected error: %v", err)
	}
	if out2 == nil || out2.ID == "" {
		t.Fatal("expected persisted message with non-empty ID")
	}
}

func TestService_SendMessage_RequiresFields(t *testing.T) {
	svc, _ := newTestService(t, "file-backend")

	// nil message
	if _, err := svc.SendMessage(context.Background(), nil); err == nil {
		t.Error("expected error for nil message")
	}

	// missing FromSessionID
	m := baseMessage("file-backend", UserSentinel)
	m.FromSessionID = ""
	if _, err := svc.SendMessage(context.Background(), m); err == nil || !strings.Contains(err.Error(), "from_session_id") {
		t.Errorf("expected from_session_id error, got: %v", err)
	}

	// missing ToSessionID
	m = baseMessage("file-backend", UserSentinel)
	m.ToSessionID = ""
	if _, err := svc.SendMessage(context.Background(), m); err == nil || !strings.Contains(err.Error(), "to_session_id") {
		t.Errorf("expected to_session_id error, got: %v", err)
	}

	// missing Body
	m = baseMessage("file-backend", UserSentinel)
	m.Body = ""
	if _, err := svc.SendMessage(context.Background(), m); err == nil || !strings.Contains(err.Error(), "body") {
		t.Errorf("expected body error, got: %v", err)
	}
}

func TestService_Inbox(t *testing.T) {
	svc, s := newTestService(t, "file-backend")

	// Seed 2 messages directly via store (bypassing validation) so we can
	// test the read path in isolation.
	for i := 0; i < 2; i++ {
		if _, err := s.SendA2AMessage(&store.A2AMessage{
			FromSessionID: "sess-other",
			FromAgentID:   UserSentinel,
			ToSessionID:   "sess-1",
			ToAgentID:     "file-backend",
			Body:          "msg",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	msgs, err := svc.Inbox(context.Background(), "sess-1", "file-backend", "")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func TestService_Thread(t *testing.T) {
	svc, s := newTestService(t, "file-backend")

	// Seed 3 messages sharing a thread_id. Spacing the inserts guarantees
	// distinct created_at values so the ORDER BY is deterministic.
	threadID := "thread-xyz"
	for i := 0; i < 3; i++ {
		if _, err := s.SendA2AMessage(&store.A2AMessage{
			FromSessionID: "sess-1",
			FromAgentID:   UserSentinel,
			ToSessionID:   "sess-1",
			ToAgentID:     "file-backend",
			ThreadID:      threadID,
			Body:          "msg",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		time.Sleep(1100 * time.Millisecond) // RFC3339 second resolution
	}

	msgs, err := svc.Thread(context.Background(), threadID)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	// Chronological order: created_at ASC.
	for i := 1; i < len(msgs); i++ {
		if msgs[i].CreatedAt < msgs[i-1].CreatedAt {
			t.Errorf("thread not chronological: %q before %q", msgs[i-1].CreatedAt, msgs[i].CreatedAt)
		}
	}
}

func TestService_Ack(t *testing.T) {
	svc, s := newTestService(t, "file-backend")
	seeded, err := s.SendA2AMessage(&store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-backend",
		Body:          "hi",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.Ack(context.Background(), "sess-1", "file-backend", seeded.ID); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	got, err := s.GetA2AMessage(seeded.ID)
	if err != nil {
		t.Fatalf("GetA2AMessage: %v", err)
	}
	if got.Status != "read" {
		t.Errorf("expected status=read, got %q", got.Status)
	}
}

func TestService_Ack_RejectsUnknownAgent(t *testing.T) {
	svc, s := newTestService(t, "file-backend")
	seeded, err := s.SendA2AMessage(&store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-backend",
		Body:          "hi",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.Ack(context.Background(), "sess-1", "file-ghost", seeded.ID); err == nil {
		t.Error("expected rejection for unknown caller agent")
	}
}

func TestService_Resolve(t *testing.T) {
	svc, s := newTestService(t, "file-backend")
	seeded, err := s.SendA2AMessage(&store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-backend",
		Body:          "hi",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.Resolve(context.Background(), "sess-1", "file-backend", seeded.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, err := s.GetA2AMessage(seeded.ID)
	if err != nil {
		t.Fatalf("GetA2AMessage: %v", err)
	}
	if got.Status != "resolved" {
		t.Errorf("expected status=resolved, got %q", got.Status)
	}
}

func TestService_RecentForSession(t *testing.T) {
	svc, s := newTestService(t, "file-backend")

	// Seed 4 messages in sess-1. Use the user sentinel on both sides so we
	// don't rely on store agent rows existing.
	for i := 0; i < 4; i++ {
		if _, err := s.SendA2AMessage(&store.A2AMessage{
			FromSessionID: "sess-1",
			FromAgentID:   UserSentinel,
			ToSessionID:   "sess-1",
			ToAgentID:     "file-backend",
			Body:          "msg",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	msgs, err := svc.RecentForSession(context.Background(), "sess-1", 2)
	if err != nil {
		t.Fatalf("RecentForSession: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
	// Store returns latest N reversed to chronological order; check it's
	// sorted ascending by created_at (ties broken deterministically by the
	// store's rowid tiebreaker, which still yields ascending created_at).
	for i := 1; i < len(msgs); i++ {
		if msgs[i].CreatedAt < msgs[i-1].CreatedAt {
			t.Errorf("recent not chronological: %q before %q", msgs[i-1].CreatedAt, msgs[i].CreatedAt)
		}
	}
}

func TestService_RecentForSession_DefaultLimit(t *testing.T) {
	// With limit=0 the Service layer substitutes its default of 20. We
	// verify by seeding more than 20 messages and confirming exactly 20
	// come back — proving the Service default (20), not the store's
	// default (50), took effect.
	svc, s := newTestService(t, "file-backend")

	for i := 0; i < 25; i++ {
		if _, err := s.SendA2AMessage(&store.A2AMessage{
			FromSessionID: "sess-1",
			FromAgentID:   UserSentinel,
			ToSessionID:   "sess-1",
			ToAgentID:     "file-backend",
			Body:          "msg",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	msgs, err := svc.RecentForSession(context.Background(), "sess-1", 0)
	if err != nil {
		t.Fatalf("RecentForSession: %v", err)
	}
	if len(msgs) != 20 {
		t.Errorf("expected default limit of 20 messages, got %d", len(msgs))
	}
}
