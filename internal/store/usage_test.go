package store

import (
	"math"
	"testing"
)

func TestRecordUsage(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	err := s.RecordUsage(sess.ID, "msg-1", "claude-sonnet-4-20250514", 1000, 500, 0, 0, 0)
	if err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	// Verify it was inserted.
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM token_usage").Scan(&count); err != nil {
		t.Fatalf("count token_usage: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}

	// Verify total_tokens and cost were computed.
	var totalTokens int
	var cost float64
	err = s.DB.QueryRow("SELECT total_tokens, estimated_cost_usd FROM token_usage WHERE message_id = 'msg-1'").Scan(&totalTokens, &cost)
	if err != nil {
		t.Fatalf("query usage record: %v", err)
	}
	if totalTokens != 1500 {
		t.Errorf("expected total_tokens=1500, got %d", totalTokens)
	}
	// Expected: 1000 * 3.0 / 1M + 500 * 15.0 / 1M = 0.003 + 0.0075 = 0.0105
	if math.Abs(cost-0.0105) > 0.0001 {
		t.Errorf("expected cost ~0.0105, got %f", cost)
	}
}

func TestGetSessionUsage(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Record multiple usage entries.
	if err := s.RecordUsage(sess.ID, "msg-1", "claude-sonnet-4-20250514", 1000, 500, 0, 0, 0); err != nil {
		t.Fatalf("RecordUsage 1: %v", err)
	}
	if err := s.RecordUsage(sess.ID, "msg-2", "claude-sonnet-4-20250514", 2000, 800, 200, 0, 0); err != nil {
		t.Fatalf("RecordUsage 2: %v", err)
	}

	summary, err := s.GetSessionUsage(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionUsage: %v", err)
	}

	if summary.InputTokens != 3000 {
		t.Errorf("expected input_tokens=3000, got %d", summary.InputTokens)
	}
	if summary.OutputTokens != 1300 {
		t.Errorf("expected output_tokens=1300, got %d", summary.OutputTokens)
	}
	if summary.TotalTokens != 4300 {
		t.Errorf("expected total_tokens=4300, got %d", summary.TotalTokens)
	}
	if summary.ToolInputTokens != 200 {
		t.Errorf("expected tool_input_tokens=200, got %d", summary.ToolInputTokens)
	}
	if summary.MessageCount != 2 {
		t.Errorf("expected message_count=2, got %d", summary.MessageCount)
	}
}

func TestGetSessionUsageEmpty(t *testing.T) {
	s := newTestStore(t)

	summary, err := s.GetSessionUsage("nonexistent")
	if err != nil {
		t.Fatalf("GetSessionUsage: %v", err)
	}

	if summary.InputTokens != 0 || summary.OutputTokens != 0 || summary.MessageCount != 0 {
		t.Errorf("expected all zeros for nonexistent session, got %+v", summary)
	}
}

func TestGetUsageSummary(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Record usage for two different models.
	if err := s.RecordUsage(sess.ID, "msg-1", "claude-sonnet-4-20250514", 1000, 500, 0, 0, 0); err != nil {
		t.Fatalf("RecordUsage 1: %v", err)
	}
	if err := s.RecordUsage(sess.ID, "msg-2", "claude-opus-4-20250514", 500, 200, 0, 0, 0); err != nil {
		t.Fatalf("RecordUsage 2: %v", err)
	}

	summary, err := s.GetUsageSummary()
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}

	if summary.TotalInput != 1500 {
		t.Errorf("expected total_input=1500, got %d", summary.TotalInput)
	}
	if summary.TotalOutput != 700 {
		t.Errorf("expected total_output=700, got %d", summary.TotalOutput)
	}
	if len(summary.ByModel) != 2 {
		t.Fatalf("expected 2 models in breakdown, got %d", len(summary.ByModel))
	}
}

func TestGetUsageSummaryEmpty(t *testing.T) {
	s := newTestStore(t)

	summary, err := s.GetUsageSummary()
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}

	if summary.TotalInput != 0 || summary.TotalOutput != 0 || summary.TotalCost != 0 {
		t.Errorf("expected all zeros, got %+v", summary)
	}
	if summary.ByModel == nil {
		t.Error("expected non-nil empty slice for ByModel")
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	// Unknown models should fall back to Sonnet pricing.
	cost := estimateCost("unknown-model", 1_000_000, 1_000_000)
	// Expected: 1M * 3.0/1M + 1M * 15.0/1M = 3.0 + 15.0 = 18.0
	if math.Abs(cost-18.0) > 0.001 {
		t.Errorf("expected cost=18.0 for unknown model, got %f", cost)
	}
}
