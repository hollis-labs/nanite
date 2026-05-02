package store

import (
	"strings"
	"testing"
)

// Glass-3 (CW-20260502-0011, SP-20260502-0001) — round-trip + enum validation
// for sessions.intent helpers. Mirrors the SQL CHECK constraint in migration
// 052_session_intent.sql.

func TestSessionIntent_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1", Title: "intent-roundtrip"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Fresh session: intent column is NULL — Get returns "".
	got, err := s.GetSessionIntent(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionIntent (fresh): %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty intent on fresh session, got %q", got)
	}

	// Round-trip each enum value.
	for _, v := range []string{
		SessionIntentLongRunning,
		SessionIntentPerTurn,
		SessionIntentEphemeral,
	} {
		if err := s.SetSessionIntent(sess.ID, v); err != nil {
			t.Fatalf("SetSessionIntent(%q): %v", v, err)
		}
		got, err := s.GetSessionIntent(sess.ID)
		if err != nil {
			t.Fatalf("GetSessionIntent after Set(%q): %v", v, err)
		}
		if got != v {
			t.Fatalf("round-trip: set %q, got %q", v, got)
		}
	}

	// Clearing back to NULL.
	if err := s.SetSessionIntent(sess.ID, ""); err != nil {
		t.Fatalf("SetSessionIntent(\"\"): %v", err)
	}
	got, err = s.GetSessionIntent(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionIntent after clear: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty intent after clear, got %q", got)
	}
}

func TestSessionIntent_RejectsInvalidValue(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1", Title: "intent-invalid"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cases := []string{
		"long-runing",  // typo
		"longrunning",  // missing hyphen
		"per_turn",     // wrong separator
		"unknown",      // not in enum
		"LONG-RUNNING", // wrong case
	}
	for _, v := range cases {
		err := s.SetSessionIntent(sess.ID, v)
		if err == nil {
			t.Fatalf("SetSessionIntent(%q): expected error, got nil", v)
		}
		if !strings.Contains(err.Error(), "invalid value") {
			t.Fatalf("SetSessionIntent(%q): unexpected error message: %v", v, err)
		}
	}

	// Confirm the invalid attempts did not change the underlying column.
	got, err := s.GetSessionIntent(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionIntent: %v", err)
	}
	if got != "" {
		t.Fatalf("expected intent to remain NULL after rejected sets, got %q", got)
	}
}

func TestSessionIntent_UnknownSession(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GetSessionIntent("does-not-exist"); err == nil {
		t.Fatalf("GetSessionIntent on missing session: expected error, got nil")
	}
	if err := s.SetSessionIntent("does-not-exist", SessionIntentLongRunning); err == nil {
		t.Fatalf("SetSessionIntent on missing session: expected error, got nil")
	}
}
