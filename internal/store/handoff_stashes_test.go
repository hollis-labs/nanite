package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestHandoffStash_UpsertAndGet(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")

	stash := HandoffStash{
		ID:        uuid.New().String(),
		SessionID: sess.ID,
		Payload:   `{"decisions_locked":["use SQLite"],"open_questions":[],"active_file_refs":[],"active_ticket_ids":["CW-001"],"should_reread":[]}`,
		CreatedAt: "2026-04-20T10:00:00Z",
	}

	if err := s.UpsertHandoffStash(stash); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.GetHandoffStash(stash.SessionID, stash.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != stash.ID {
		t.Errorf("id: got %q, want %q", got.ID, stash.ID)
	}
	if got.SessionID != stash.SessionID {
		t.Errorf("session_id: got %q, want %q", got.SessionID, stash.SessionID)
	}
	if got.Payload != stash.Payload {
		t.Errorf("payload mismatch: got %q, want %q", got.Payload, stash.Payload)
	}
}

func TestHandoffStash_Upsert_UpdatesPayload(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")
	id := uuid.New().String()

	first := HandoffStash{ID: id, SessionID: sess.ID,
		Payload:   `{"decisions_locked":["v1"],"open_questions":[],"active_file_refs":[],"active_ticket_ids":[],"should_reread":[]}`,
		CreatedAt: "2026-04-20T10:00:00Z"}
	second := HandoffStash{ID: id, SessionID: sess.ID,
		Payload:   `{"decisions_locked":["v2"],"open_questions":[],"active_file_refs":[],"active_ticket_ids":[],"should_reread":[]}`,
		CreatedAt: "2026-04-20T11:00:00Z"}

	if err := s.UpsertHandoffStash(first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := s.UpsertHandoffStash(second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := s.GetHandoffStash(sess.ID, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Payload != second.Payload {
		t.Errorf("payload after update: got %q, want %q", got.Payload, second.Payload)
	}
}

func TestHandoffStash_GetLatestStashForSession(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")

	older := HandoffStash{ID: uuid.New().String(), SessionID: sess.ID,
		Payload:   `{"decisions_locked":["older"],"open_questions":[],"active_file_refs":[],"active_ticket_ids":[],"should_reread":[]}`,
		CreatedAt: "2026-04-20T10:00:00Z"}
	newer := HandoffStash{ID: uuid.New().String(), SessionID: sess.ID,
		Payload:   `{"decisions_locked":["newer"],"open_questions":[],"active_file_refs":[],"active_ticket_ids":[],"should_reread":[]}`,
		CreatedAt: "2026-04-20T11:00:00Z"}

	if err := s.UpsertHandoffStash(older); err != nil {
		t.Fatalf("upsert older: %v", err)
	}
	if err := s.UpsertHandoffStash(newer); err != nil {
		t.Fatalf("upsert newer: %v", err)
	}

	got, err := s.GetLatestStashForSession(sess.ID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got.ID != newer.ID {
		t.Errorf("latest id: got %q, want %q", got.ID, newer.ID)
	}
}

func TestHandoffStash_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetHandoffStash("no-such-session", "no-such-id")
	if !errors.Is(err, ErrHandoffStashNotFound) {
		t.Errorf("expected ErrHandoffStashNotFound, got %v", err)
	}
}

func TestHandoffStash_GetLatestNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetLatestStashForSession("empty-session")
	if !errors.Is(err, ErrHandoffStashNotFound) {
		t.Errorf("expected ErrHandoffStashNotFound, got %v", err)
	}
}
