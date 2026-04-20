package store

import (
	"errors"
	"testing"
)

func TestSessionObject_PutAndGet(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")

	obj, err := s.PutSessionObject(SessionObjectInput{
		SessionID:   sess.ID,
		ContentType: "application/json",
		Payload:     `{"kind":"card","text":"hello"}`,
	})
	if err != nil {
		t.Fatalf("PutSessionObject: %v", err)
	}
	if obj.ID == "" {
		t.Error("expected non-empty ID")
	}
	if len(obj.ID) != 26 {
		t.Errorf("expected 26-char ULID, got %d chars: %q", len(obj.ID), obj.ID)
	}
	if obj.ByteSize != len(`{"kind":"card","text":"hello"}`) {
		t.Errorf("ByteSize mismatch: got %d, want %d", obj.ByteSize, len(`{"kind":"card","text":"hello"}`))
	}
	if obj.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be populated")
	}

	got, err := s.GetSessionObject(sess.ID, obj.ID)
	if err != nil {
		t.Fatalf("GetSessionObject: %v", err)
	}
	if got.Payload != `{"kind":"card","text":"hello"}` {
		t.Errorf("Payload mismatch: got %q", got.Payload)
	}
	if got.ContentType != "application/json" {
		t.Errorf("ContentType mismatch: got %q", got.ContentType)
	}
}

func TestSessionObject_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")

	_, err := s.GetSessionObject(sess.ID, "01HQ0000000000000000000000")
	if !errors.Is(err, ErrSessionObjectNotFound) {
		t.Errorf("expected ErrSessionObjectNotFound, got %v", err)
	}
}

func TestSessionObject_CrossSessionIsolation(t *testing.T) {
	// D5: A session B lookup for an object created in session A must return
	// ErrSessionObjectNotFound, NOT the A-scoped row.
	s := newTestStore(t)
	sessA := makeTestSession(t, s, "workspace-1")
	sessB := makeTestSession(t, s, "workspace-1")

	obj, err := s.PutSessionObject(SessionObjectInput{
		SessionID: sessA.ID,
		Payload:   `{"a":1}`,
	})
	if err != nil {
		t.Fatalf("PutSessionObject: %v", err)
	}

	_, err = s.GetSessionObject(sessB.ID, obj.ID)
	if !errors.Is(err, ErrSessionObjectNotFound) {
		t.Errorf("D5 violation: cross-session lookup must return ErrSessionObjectNotFound, got %v", err)
	}

	// Control: same id + owning session works.
	if _, err := s.GetSessionObject(sessA.ID, obj.ID); err != nil {
		t.Errorf("same-session lookup failed: %v", err)
	}
}

func TestSessionObject_SizeCapEnforced(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")

	// Build a payload larger than DefaultSessionObjectMaxBytes.
	big := make([]byte, DefaultSessionObjectMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	payload := `{"x":"` + string(big) + `"}`

	_, err := s.PutSessionObject(SessionObjectInput{
		SessionID: sess.ID,
		Payload:   payload,
	})
	if !errors.Is(err, ErrSessionObjectTooLarge) {
		t.Errorf("expected ErrSessionObjectTooLarge, got %v", err)
	}
}

func TestSessionObject_List(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")

	// Ordering determinism comes from the SQL tiebreak on `id DESC` — ULIDs
	// are monotonic within a millisecond, so no sleep is needed between puts.
	for i := 0; i < 3; i++ {
		if _, err := s.PutSessionObject(SessionObjectInput{
			SessionID: sess.ID,
			Payload:   `{}`,
		}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}

	list, err := s.ListSessionObjects(sess.ID)
	if err != nil {
		t.Fatalf("ListSessionObjects: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 records, got %d", len(list))
	}
	// Ordered DESC by created_at — assert monotonic.
	for i := 1; i < len(list); i++ {
		if list[i].CreatedAt.After(list[i-1].CreatedAt) {
			t.Errorf("ListSessionObjects not DESC by created_at at i=%d", i)
		}
	}
}

func TestSessionObject_List_OtherSessionExcluded(t *testing.T) {
	s := newTestStore(t)
	sessA := makeTestSession(t, s, "workspace-1")
	sessB := makeTestSession(t, s, "workspace-1")

	if _, err := s.PutSessionObject(SessionObjectInput{SessionID: sessA.ID, Payload: `{}`}); err != nil {
		t.Fatalf("put A: %v", err)
	}
	if _, err := s.PutSessionObject(SessionObjectInput{SessionID: sessB.ID, Payload: `{}`}); err != nil {
		t.Fatalf("put B: %v", err)
	}

	list, err := s.ListSessionObjects(sessA.ID)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 A-scoped record, got %d", len(list))
	}
}

func TestSessionObject_EvictSessionObjects(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")

	obj, err := s.PutSessionObject(SessionObjectInput{SessionID: sess.ID, Payload: `{}`})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	n, err := s.EvictSessionObjects(sess.ID)
	if err != nil {
		t.Fatalf("EvictSessionObjects: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row evicted, got %d", n)
	}

	_, err = s.GetSessionObject(sess.ID, obj.ID)
	if !errors.Is(err, ErrSessionObjectNotFound) {
		t.Errorf("expected not-found after evict, got %v", err)
	}
}

func TestSessionObject_PutSessionIDRequired(t *testing.T) {
	s := newTestStore(t)
	_, err := s.PutSessionObject(SessionObjectInput{SessionID: "", Payload: `{}`})
	if err == nil {
		t.Error("expected error on empty session_id")
	}
}

func TestSessionObject_PutPayloadRequired(t *testing.T) {
	s := newTestStore(t)
	sess := makeTestSession(t, s, "workspace-1")
	_, err := s.PutSessionObject(SessionObjectInput{SessionID: sess.ID, Payload: ""})
	if err == nil {
		t.Error("expected error on empty payload")
	}
}

func TestSessionObject_EndToEnd_ArchiveEvictsAndCrossSessionFails(t *testing.T) {
	// Full P2 contract walk:
	//   1. Put object in session A.
	//   2. Get succeeds from A, fails from B (D5).
	//   3. Archive A — object disappears; session-B object unaffected.
	//   4. Post-archive Get from A returns ErrSessionObjectNotFound.
	s := newTestStore(t)

	sessA := makeTestSession(t, s, "workspace-1")
	sessB := makeTestSession(t, s, "workspace-1")

	objA, err := s.PutSessionObject(SessionObjectInput{
		SessionID:   sessA.ID,
		ContentType: "application/json",
		Payload:     `{"kind":"card","id":"abc"}`,
	})
	if err != nil {
		t.Fatalf("put A: %v", err)
	}
	objB, err := s.PutSessionObject(SessionObjectInput{
		SessionID: sessB.ID,
		Payload:   `{"keep":"me"}`,
	})
	if err != nil {
		t.Fatalf("put B: %v", err)
	}

	// 2. In-session lookup works; cross-session fails.
	if _, err := s.GetSessionObject(sessA.ID, objA.ID); err != nil {
		t.Fatalf("A lookup: %v", err)
	}
	if _, err := s.GetSessionObject(sessB.ID, objA.ID); !errors.Is(err, ErrSessionObjectNotFound) {
		t.Errorf("D5: cross-session lookup must fail, got %v", err)
	}

	// 3. Archive A.
	if err := s.ArchiveSession(sessA.ID); err != nil {
		t.Fatalf("archive A: %v", err)
	}

	// 4. A's object is unreachable from its own session; B's is untouched.
	if _, err := s.GetSessionObject(sessA.ID, objA.ID); !errors.Is(err, ErrSessionObjectNotFound) {
		t.Errorf("post-archive: expected not-found for A's object, got %v", err)
	}
	if _, err := s.GetSessionObject(sessB.ID, objB.ID); err != nil {
		t.Errorf("post-archive: B's object unexpectedly gone: %v", err)
	}
}

// makeTestSession helper — creates a real session row so FK constraints hold.
// Delegates workspace seeding to seedWorkspace (idempotent), so multiple
// sessions can share the same workspace without colliding on the workspace row.
func makeTestSession(t *testing.T, s *Store, workspaceID string) *Session {
	t.Helper()
	if workspaceID != "" {
		seedWorkspace(t, s, workspaceID)
	}
	sess := &Session{
		WorkspaceID: workspaceID,
		Status:      "active",
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}
