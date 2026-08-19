package store

import (
	"database/sql"
	"errors"
	"testing"
)

// seedEnvelopeTestSession inserts the session row the
// envelope_instances.session_id foreign key requires.
func seedEnvelopeTestSession(t *testing.T, s *Store, sessionID string) {
	t.Helper()
	if _, err := s.DB.Exec(
		`INSERT INTO sessions (id, title, short_code, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		sessionID, "t", "sc-"+sessionID,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func TestEnvelopeInstance_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	seedEnvelopeTestSession(t, s, "sess-1")

	inst := &EnvelopeInstance{
		SessionID:    "sess-1",
		EnvelopeType: "metric-card",
		EnvelopeJSON: `{"kind":"envelope","version":1,"type":"metric-card"}`,
	}
	if err := s.CreateEnvelopeInstance(inst); err != nil {
		t.Fatalf("create: %v", err)
	}
	if inst.ID == "" {
		t.Fatal("expected ID to be assigned")
	}
	if inst.EmittedAt.IsZero() {
		t.Fatal("expected emitted_at to be assigned")
	}

	got, err := s.GetEnvelopeInstance(inst.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SessionID != "sess-1" || got.EnvelopeType != "metric-card" {
		t.Fatalf("fields not preserved: %+v", got)
	}
	if got.RespondedAt != nil || got.ResponseStatus != "" || got.ResponseJSON != "" {
		t.Fatalf("expected unresponded envelope, got %+v", got)
	}
}

func TestEnvelopeInstance_RecordResponse(t *testing.T) {
	s := newTestStore(t)
	seedEnvelopeTestSession(t, s, "sess-1")

	inst := &EnvelopeInstance{
		SessionID:    "sess-1",
		EnvelopeType: "collect_feedback",
		EnvelopeJSON: `{}`,
	}
	if err := s.CreateEnvelopeInstance(inst); err != nil {
		t.Fatalf("create: %v", err)
	}

	respJSON := `{"v":1,"kind":"collect_feedback","id":"` + inst.ID + `","status":"submitted"}`
	if err := s.RecordResponse(inst.ID, "submitted", respJSON); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := s.GetEnvelopeInstance(inst.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RespondedAt == nil {
		t.Fatal("expected responded_at set")
	}
	if got.ResponseStatus != "submitted" {
		t.Fatalf("status: %q", got.ResponseStatus)
	}
	if got.ResponseJSON != respJSON {
		t.Fatalf("response_json: %q", got.ResponseJSON)
	}
}

func TestEnvelopeInstance_DuplicateResponseRejected(t *testing.T) {
	s := newTestStore(t)
	seedEnvelopeTestSession(t, s, "s")

	inst := &EnvelopeInstance{SessionID: "s", EnvelopeType: "t", EnvelopeJSON: "{}"}
	if err := s.CreateEnvelopeInstance(inst); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.RecordResponse(inst.ID, "submitted", `{"v":1}`); err != nil {
		t.Fatalf("first record: %v", err)
	}
	err := s.RecordResponse(inst.ID, "submitted", `{"v":1}`)
	if !errors.Is(err, ErrEnvelopeAlreadyResponded) {
		t.Fatalf("expected ErrEnvelopeAlreadyResponded, got %v", err)
	}
}

func TestEnvelopeInstance_GetMissing(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetEnvelopeInstance("nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}
