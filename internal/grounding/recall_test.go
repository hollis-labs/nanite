package grounding_test

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/grounding"
)

// stubLogger is a minimal ConsultationLogger for tests.
type stubLogger struct {
	consultations []grounding.ConsultationEntry
	outcomes      []grounding.Outcome
	nextID        int64
}

func (l *stubLogger) LogGroundingConsultation(ctx context.Context, entry grounding.ConsultationEntry) (int64, error) {
	l.consultations = append(l.consultations, entry)
	l.nextID++
	return l.nextID, nil
}

func (l *stubLogger) LogGroundingOutcome(ctx context.Context, outcome grounding.Outcome) error {
	l.outcomes = append(l.outcomes, outcome)
	return nil
}

// TestRecall_GateOff verifies that when NANITE_GROUNDING_ENABLED is not set
// (the default), Recall returns Enabled=false and does not call the logger.
func TestRecall_GateOff(t *testing.T) {
	// Gate is not set in test environment by default.
	t.Setenv("NANITE_GROUNDING_ENABLED", "false")

	r := grounding.NewRecaller(nil, 5)
	result := r.Recall(context.Background(), grounding.RecallInput{
		UserInput: "tell me about the project",
		SessionID: "sess-1",
	})
	if result.Enabled {
		t.Fatal("expected Enabled=false when gate is off")
	}
	if len(result.Hits) != 0 {
		t.Fatalf("expected no hits, got %d", len(result.Hits))
	}
}

// TestRecall_GateOn_NilService verifies that when gate is on but the memory
// service is nil, Recall returns Enabled=true with empty hits.
func TestRecall_GateOn_NilService(t *testing.T) {
	t.Setenv("NANITE_GROUNDING_ENABLED", "true")

	r := grounding.NewRecaller(nil, 5) // nil svc
	result := r.Recall(context.Background(), grounding.RecallInput{
		UserInput: "anything",
		SessionID: "sess-2",
	})
	if !result.Enabled {
		t.Fatal("expected Enabled=true when gate is on")
	}
	if len(result.Hits) != 0 {
		t.Fatalf("expected no hits from nil svc, got %d", len(result.Hits))
	}
	if result.TimedOut {
		t.Fatal("nil svc should not set TimedOut")
	}
}

// TestGroundingNamespace verifies the namespace convention.
func TestGroundingNamespace(t *testing.T) {
	cases := []struct {
		user string
		want string
	}{
		{"alice", "user/alice/project/nanite/memory"},
		{"", "user/default/project/nanite/memory"},
	}
	for _, tc := range cases {
		got := grounding.GroundingNamespace(tc.user)
		if got != tc.want {
			t.Errorf("GroundingNamespace(%q) = %q, want %q", tc.user, got, tc.want)
		}
	}
}

// TestSystemPromptBlock_Empty verifies that an empty/disabled result
// produces no block.
func TestSystemPromptBlock_Empty(t *testing.T) {
	result := grounding.GroundingResult{Enabled: true, Surfaced: nil}
	block := grounding.SystemPromptBlock(result)
	if block != "" {
		t.Errorf("expected empty block, got %q", block)
	}
}

// TestSystemPromptBlock_Disabled verifies that a disabled result produces no block.
func TestSystemPromptBlock_Disabled(t *testing.T) {
	result := grounding.GroundingResult{Enabled: false}
	block := grounding.SystemPromptBlock(result)
	if block != "" {
		t.Errorf("expected empty block for disabled result, got %q", block)
	}
}

// TestSystemPromptBlock_WithHits verifies that surfaced hits produce the
// expected "## Relevant memories" block.
func TestSystemPromptBlock_WithHits(t *testing.T) {
	result := grounding.GroundingResult{
		Enabled: true,
		Surfaced: []grounding.MemoryHit{
			{Summary: "User prefers concise answers"},
			{Summary: "Project uses SQLite for storage"},
		},
	}
	block := grounding.SystemPromptBlock(result)
	if block == "" {
		t.Fatal("expected non-empty block")
	}
	if block[:21] != "## Relevant memories\n" {
		t.Errorf("unexpected block header: %q", block[:21])
	}
	if !containsStr(block, "User prefers concise answers") {
		t.Error("expected first summary in block")
	}
	if !containsStr(block, "Project uses SQLite for storage") {
		t.Error("expected second summary in block")
	}
}

// TestLogConsultations_NilLogger verifies no panic when logger is nil.
func TestLogConsultations_NilLogger(t *testing.T) {
	result := grounding.GroundingResult{
		Enabled:  true,
		Hits:     []grounding.MemoryHit{{MemoryKey: "k1", Similarity: 0.8}},
		Surfaced: []grounding.MemoryHit{{MemoryKey: "k1", Similarity: 0.8}},
	}
	// Should not panic.
	ids := grounding.LogConsultations(nil, result, "t1")
	if ids != nil {
		t.Errorf("nil logger: expected nil ids, got %v", ids)
	}
}

// TestLogConsultations_DisabledResult verifies no rows are logged for disabled results.
func TestLogConsultations_DisabledResult(t *testing.T) {
	logger := &stubLogger{}
	result := grounding.GroundingResult{Enabled: false}
	ids := grounding.LogConsultations(logger, result, "t1")
	if len(logger.consultations) != 0 {
		t.Errorf("disabled result: expected no consultations, got %d", len(logger.consultations))
	}
	if ids != nil {
		t.Errorf("disabled result: expected nil ids, got %v", ids)
	}
}

// TestLogConsultations_MarksConsumedCorrectly verifies that hits above the
// threshold are marked consumed=true and hits below are consumed=false.
func TestLogConsultations_MarksConsumedCorrectly(t *testing.T) {
	logger := &stubLogger{}
	hit1 := grounding.MemoryHit{MemoryKey: "k1", Summary: "s1", Similarity: 0.9}
	hit2 := grounding.MemoryHit{MemoryKey: "k2", Summary: "s2", Similarity: 0.3} // below threshold

	result := grounding.GroundingResult{
		Enabled:   true,
		Hits:      []grounding.MemoryHit{hit1, hit2},
		Surfaced:  []grounding.MemoryHit{hit1}, // only k1 surfaced
		SessionID: "sess-3",
	}

	ids := grounding.LogConsultations(logger, result, "turn-1")

	if len(logger.consultations) != 2 {
		t.Fatalf("expected 2 consultation rows, got %d", len(logger.consultations))
	}
	// k1 should be consumed
	found := false
	for _, c := range logger.consultations {
		if c.MemoryKey == "k1" {
			found = true
			if !c.Consumed {
				t.Error("k1 should be consumed=true")
			}
		}
		if c.MemoryKey == "k2" && c.Consumed {
			t.Error("k2 should be consumed=false")
		}
	}
	if !found {
		t.Error("k1 not found in consultations")
	}
	// Only surfaced IDs returned.
	if len(ids) != 1 {
		t.Errorf("expected 1 surfaced ID, got %d", len(ids))
	}
}

// TestRecordOutcome_NilLogger verifies no panic when logger is nil.
func TestRecordOutcome_NilLogger(t *testing.T) {
	// Should not panic.
	grounding.RecordOutcome(nil, []int64{1, 2}, "just give me less", time.Now())
}

// TestRecordOutcome_EmptyIDs verifies no-op when no IDs provided.
func TestRecordOutcome_EmptyIDs(t *testing.T) {
	logger := &stubLogger{}
	grounding.RecordOutcome(logger, nil, "just less", time.Now())
	if len(logger.outcomes) != 0 {
		t.Errorf("expected no outcomes for nil IDs, got %d", len(logger.outcomes))
	}
}

// TestRecordOutcome_EmptyFollowUp verifies no-op when follow-up is empty.
func TestRecordOutcome_EmptyFollowUp(t *testing.T) {
	logger := &stubLogger{}
	grounding.RecordOutcome(logger, []int64{1}, "", time.Now())
	if len(logger.outcomes) != 0 {
		t.Errorf("expected no outcomes for empty follow-up, got %d", len(logger.outcomes))
	}
}

// TestRecordOutcome_WritesOneRowPerID verifies that one outcome row is written
// per surfaced consultation ID.
func TestRecordOutcome_WritesOneRowPerID(t *testing.T) {
	logger := &stubLogger{}
	ids := []int64{10, 11, 12}
	grounding.RecordOutcome(logger, ids, "just the short version", time.Now())
	if len(logger.outcomes) != 3 {
		t.Fatalf("expected 3 outcome rows, got %d", len(logger.outcomes))
	}
	for i, o := range logger.outcomes {
		if o.ConsultationID != ids[i] {
			t.Errorf("row %d: want consultation_id %d, got %d", i, ids[i], o.ConsultationID)
		}
		if o.Kind != grounding.OutcomeRefined {
			t.Errorf("row %d: expected OutcomeRefined for pruning word 'just', got %q", i, o.Kind)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
