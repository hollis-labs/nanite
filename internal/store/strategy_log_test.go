package store

import (
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/strategy"
)

// TestLogStrategyDecision_RoundTrip verifies one row inserts, persists,
// and reads back with all fields intact (including the JSON-encoded
// grounding_consultation_ids slice).
func TestLogStrategyDecision_RoundTrip(t *testing.T) {
	s := newTestStore(t)

	entry := strategy.DecisionEntry{
		SessionID:                "sess-1",
		TurnID:                   "msg-7",
		Approach:                 strategy.ApproachSubagentDelegation,
		Rationale:                "intent: large → subagent_delegation",
		MaxTurns:                 40,
		EscalationBudget:         0,
		ReflexMatchID:            "background-long-task",
		PlaybookHit:              "",
		GroundingConsultationIDs: []int64{11, 22, 33},
	}

	id, err := s.LogStrategyDecision(entry)
	if err != nil {
		t.Fatalf("LogStrategyDecision: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected id > 0, got %d", id)
	}

	rows, err := s.ListStrategyDecisions("sess-1")
	if err != nil {
		t.Fatalf("ListStrategyDecisions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}
	r := rows[0]
	if r.ID != id {
		t.Errorf("id: got %d, want %d", r.ID, id)
	}
	if r.SessionID != "sess-1" {
		t.Errorf("session_id: got %q", r.SessionID)
	}
	if r.TurnID != "msg-7" {
		t.Errorf("turn_id: got %q", r.TurnID)
	}
	if r.Approach != string(strategy.ApproachSubagentDelegation) {
		t.Errorf("approach: got %q", r.Approach)
	}
	if r.MaxTurns != 40 {
		t.Errorf("max_turns: got %d", r.MaxTurns)
	}
	if r.ReflexMatchID != "background-long-task" {
		t.Errorf("reflex_match_id: got %q", r.ReflexMatchID)
	}
	if !reflect.DeepEqual(r.GroundingConsultationIDs, []int64{11, 22, 33}) {
		t.Errorf("ids: got %v, want [11 22 33]", r.GroundingConsultationIDs)
	}
	if r.CreatedAt == "" {
		t.Errorf("expected non-empty created_at")
	}
}

// TestLogStrategyDecision_OptionalFieldsNullable verifies that a row
// with no turn_id, no reflex match, and an empty grounding ID slice
// reads back cleanly (NULLs surface as empty strings; ID slice is
// nil).
func TestLogStrategyDecision_OptionalFieldsNullable(t *testing.T) {
	s := newTestStore(t)

	entry := strategy.DecisionEntry{
		SessionID:                "sess-2",
		Approach:                 strategy.ApproachDirectChain,
		Rationale:                "default",
		MaxTurns:                 20,
		GroundingConsultationIDs: nil,
	}
	if _, err := s.LogStrategyDecision(entry); err != nil {
		t.Fatalf("LogStrategyDecision: %v", err)
	}

	rows, err := s.ListStrategyDecisions("sess-2")
	if err != nil {
		t.Fatalf("ListStrategyDecisions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}
	r := rows[0]
	if r.TurnID != "" {
		t.Errorf("expected empty turn_id, got %q", r.TurnID)
	}
	if r.ReflexMatchID != "" {
		t.Errorf("expected empty reflex_match_id, got %q", r.ReflexMatchID)
	}
	if r.PlaybookHit != "" {
		t.Errorf("expected empty playbook_hit, got %q", r.PlaybookHit)
	}
	if len(r.GroundingConsultationIDs) != 0 {
		t.Errorf("expected empty ids, got %v", r.GroundingConsultationIDs)
	}
}

// TestLogStrategyDecision_RationaleTruncated verifies the 1024-char
// rationale truncation guard.
func TestLogStrategyDecision_RationaleTruncated(t *testing.T) {
	s := newTestStore(t)
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'x'
	}
	entry := strategy.DecisionEntry{
		SessionID: "sess-3",
		Approach:  strategy.ApproachDirectChain,
		Rationale: string(long),
		MaxTurns:  20,
	}
	if _, err := s.LogStrategyDecision(entry); err != nil {
		t.Fatalf("LogStrategyDecision: %v", err)
	}
	rows, err := s.ListStrategyDecisions("sess-3")
	if err != nil {
		t.Fatalf("ListStrategyDecisions: %v", err)
	}
	if len(rows[0].Rationale) != 1024 {
		t.Errorf("rationale length: got %d, want 1024", len(rows[0].Rationale))
	}
}

// TestLogStrategyDecision_FromStrategyHelper round-trips a Strategy
// through FromStrategy → LogStrategyDecision and verifies the
// conversion preserves all observable fields.
func TestLogStrategyDecision_FromStrategyHelper(t *testing.T) {
	s := newTestStore(t)
	plan := strategy.Strategy{
		Approach:                 strategy.ApproachAskToClarify,
		Rationale:                "ambiguous",
		MaxTurns:                 5,
		EscalationBudget:         0,
		ReflexMatchID:            "researcher-mention",
		PlaybookHit:              "",
		GroundingConsultationIDs: []int64{99},
	}
	entry := strategy.FromStrategy(plan, "sess-4", "turn-1")
	if _, err := s.LogStrategyDecision(entry); err != nil {
		t.Fatalf("LogStrategyDecision: %v", err)
	}
	rows, _ := s.ListStrategyDecisions("sess-4")
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}
	if rows[0].Approach != string(strategy.ApproachAskToClarify) {
		t.Errorf("approach mismatch: %q", rows[0].Approach)
	}
	if rows[0].TurnID != "turn-1" {
		t.Errorf("turn_id: got %q", rows[0].TurnID)
	}
}
