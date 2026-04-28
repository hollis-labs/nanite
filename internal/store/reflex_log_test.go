package store

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/reflex"
)

func TestLogReflexMatch_RoundTrip(t *testing.T) {
	s := newTestStore(t)

	entry := reflex.ReflexMatchEntry{
		SessionID:           "sess-integration-001",
		TurnID:              "turn-001",
		ReflexID:            "planner-mention",
		Priority:            20,
		Source:              "reflex",
		MatchedInputExcerpt: "let's plan out the migration",
		HintTier:            "open",
		HintPattern:         "subagent",
		ProfileSlug:         "planner",
		Mode:                "planning",
	}

	if err := s.LogReflexMatch(entry); err != nil {
		t.Fatalf("LogReflexMatch: %v", err)
	}

	rows, err := s.ListReflexMatchLog("sess-integration-001")
	if err != nil {
		t.Fatalf("ListReflexMatchLog: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	r := rows[0]
	if r.SessionID != entry.SessionID {
		t.Errorf("SessionID = %q, want %q", r.SessionID, entry.SessionID)
	}
	if r.TurnID != entry.TurnID {
		t.Errorf("TurnID = %q, want %q", r.TurnID, entry.TurnID)
	}
	if r.ReflexID != entry.ReflexID {
		t.Errorf("ReflexID = %q, want %q", r.ReflexID, entry.ReflexID)
	}
	if r.Priority != entry.Priority {
		t.Errorf("Priority = %d, want %d", r.Priority, entry.Priority)
	}
	if r.Source != "reflex" {
		t.Errorf("Source = %q, want reflex", r.Source)
	}
	if r.HintTier != entry.HintTier {
		t.Errorf("HintTier = %q, want %q", r.HintTier, entry.HintTier)
	}
	if r.ProfileSlug != entry.ProfileSlug {
		t.Errorf("ProfileSlug = %q, want %q", r.ProfileSlug, entry.ProfileSlug)
	}
}

func TestLogReflexMatch_NullTurnID(t *testing.T) {
	s := newTestStore(t)

	// TurnID="" should store as NULL and come back as "".
	entry := reflex.ReflexMatchEntry{
		SessionID: "sess-integration-002",
		TurnID:    "",
		ReflexID:  "worker-execute",
		Priority:  10,
		Source:    "reflex",
	}
	if err := s.LogReflexMatch(entry); err != nil {
		t.Fatalf("LogReflexMatch: %v", err)
	}
	rows, err := s.ListReflexMatchLog("sess-integration-002")
	if err != nil {
		t.Fatalf("ListReflexMatchLog: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].TurnID != "" {
		t.Errorf("TurnID = %q, want empty string (NULL coalesced)", rows[0].TurnID)
	}
}

func TestLogReflexMatch_MultipleRows(t *testing.T) {
	s := newTestStore(t)

	for i, reflexID := range []string{"planner-mention", "researcher-mention", "worker-execute"} {
		_ = i
		if err := s.LogReflexMatch(reflex.ReflexMatchEntry{
			SessionID: "sess-integration-003",
			ReflexID:  reflexID,
			Source:    "reflex",
		}); err != nil {
			t.Fatalf("LogReflexMatch(%s): %v", reflexID, err)
		}
	}

	rows, err := s.ListReflexMatchLog("sess-integration-003")
	if err != nil {
		t.Fatalf("ListReflexMatchLog: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// Verify insertion order (id asc).
	want := []string{"planner-mention", "researcher-mention", "worker-execute"}
	for i, r := range rows {
		if r.ReflexID != want[i] {
			t.Errorf("rows[%d].ReflexID = %q, want %q", i, r.ReflexID, want[i])
		}
	}
}

func TestListReflexMatchLog_OtherSessionIsolated(t *testing.T) {
	s := newTestStore(t)

	_ = s.LogReflexMatch(reflex.ReflexMatchEntry{
		SessionID: "sess-A",
		ReflexID:  "planner-mention",
		Source:    "reflex",
	})
	_ = s.LogReflexMatch(reflex.ReflexMatchEntry{
		SessionID: "sess-B",
		ReflexID:  "worker-execute",
		Source:    "reflex",
	})

	rowsA, err := s.ListReflexMatchLog("sess-A")
	if err != nil {
		t.Fatalf("ListReflexMatchLog: %v", err)
	}
	if len(rowsA) != 1 || rowsA[0].ReflexID != "planner-mention" {
		t.Errorf("sess-A isolation failed: %v", rowsA)
	}

	rowsB, err := s.ListReflexMatchLog("sess-B")
	if err != nil {
		t.Fatalf("ListReflexMatchLog: %v", err)
	}
	if len(rowsB) != 1 || rowsB[0].ReflexID != "worker-execute" {
		t.Errorf("sess-B isolation failed: %v", rowsB)
	}
}
