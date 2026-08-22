package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRecordExecutionMetrics(t *testing.T) {
	s, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	m := &ExecutionMetrics{
		SessionID:       "sess-1",
		MessageID:       "msg-1",
		Provider:        "anthropic",
		Adapter:         "http",
		Model:           "claude-sonnet-4-20250514",
		AgentID:         "agent-1",
		AgentSlug:       "mentat",
		Mode:            "default",
		DurationMs:      1500,
		ContextMessages: 10,
		ContextTokens:   5000,
		InputTokens:     3000,
		OutputTokens:    1000,
		ToolIterations:  2,
		ToolCalls:       3,
		StopReason:      "end_turn",
	}

	if err := s.RecordExecutionMetrics(context.Background(), m); err != nil {
		t.Fatalf("RecordExecutionMetrics: %v", err)
	}

	// Verify cost was calculated.
	if m.EstimatedCostUSD <= 0 {
		t.Error("expected non-zero estimated cost")
	}
}

func TestGetSessionExecutionMetrics(t *testing.T) {
	s, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.

		// Record two metrics for the same session.
		Background())

	for i, msgID := range []string{"msg-1", "msg-2"} {
		m := &ExecutionMetrics{
			SessionID:  "sess-1",
			MessageID:  msgID,
			Provider:   "anthropic",
			Adapter:    "http",
			Model:      "claude-sonnet-4-20250514",
			DurationMs: int64((i + 1) * 1000),
		}
		if err := s.RecordExecutionMetrics(context.Background(), m); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	// Record one for a different session.
	if err := s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
		SessionID: "sess-2",
		MessageID: "msg-3",
		Provider:  "ollama",
		Adapter:   "http",
	}); err != nil {
		t.Fatal(err)
	}

	metrics, err := s.GetSessionExecutionMetrics(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("GetSessionExecutionMetrics: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics for sess-1, got %d", len(metrics))
	}
	// Verify both messages are present (order may vary with identical timestamps).
	ids := map[string]bool{metrics[0].MessageID: true, metrics[1].MessageID: true}
	if !ids["msg-1"] || !ids["msg-2"] {
		t.Errorf("expected msg-1 and msg-2, got %v", ids)
	}
}

func TestGetRecentExecutionMetrics(t *testing.T) {
	s, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	for i, msgID := range []string{"msg-1", "msg-2", "msg-3"} {
		if err := s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
			SessionID:  "sess-1",
			MessageID:  msgID,
			Provider:   "anthropic",
			DurationMs: int64((i + 1) * 500),
		}); err != nil {
			t.Fatal(err)
		}
	}

	metrics, err := s.GetRecentExecutionMetrics(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetRecentExecutionMetrics: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("expected 2 with limit=2, got %d", len(metrics))
	}
}

func TestGetUtilityCallSummary(t *testing.T) {
	s, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.

		// Record utility calls from two providers.
		Background())

	for i := 0; i < 3; i++ {
		s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
			SessionID:  "sess-1",
			MessageID:  "autoTitle",
			Provider:   "anthropic",
			Model:      "claude-sonnet-4-20250514",
			DurationMs: int64(500 + i*100),
			IsUtility:  true,
		})
	}
	s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
		SessionID:  "sess-2",
		MessageID:  "autoTitle",
		Provider:   "ollama",
		Model:      "llama3",
		DurationMs: 2000,
		IsUtility:  true,
		Error:      "timeout",
	})
	// Non-utility call should be excluded.
	s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
		SessionID:  "sess-1",
		MessageID:  "msg-1",
		Provider:   "anthropic",
		DurationMs: 1000,
		IsUtility:  false,
	})

	summary, err := s.GetUtilityCallSummary(context.Background())
	if err != nil {
		t.Fatalf("GetUtilityCallSummary: %v", err)
	}
	if len(summary) != 2 {
		t.Fatalf("expected 2 summary rows, got %d", len(summary))
	}

	// First should be anthropic (3 calls) since ordered by call_count DESC.
	if summary[0].CallCount != 3 {
		t.Errorf("expected 3 calls for first row, got %d", summary[0].CallCount)
	}
	if summary[0].Provider != "anthropic" {
		t.Errorf("expected anthropic first, got %s", summary[0].Provider)
	}
	if summary[1].ErrorCount != 1 {
		t.Errorf("expected 1 error for ollama, got %d", summary[1].ErrorCount)
	}
}

func TestGetUtilityCallLog(t *testing.T) {
	s, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
		SessionID: "sess-1", MessageID: "autoTitle", Provider: "anthropic", IsUtility: true,
	})
	s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
		SessionID: "sess-1", MessageID: "autoTags", Provider: "anthropic", IsUtility: true,
	})
	// Non-utility — should be excluded.
	s.RecordExecutionMetrics(context.Background(), &ExecutionMetrics{
		SessionID: "sess-1", MessageID: "msg-1", Provider: "anthropic", IsUtility: false,
	})

	log, err := s.GetUtilityCallLog(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetUtilityCallLog: %v", err)
	}
	if len(log) != 2 {
		t.Fatalf("expected 2 utility calls, got %d", len(log))
	}
	for _, m := range log {
		if !m.IsUtility {
			t.Error("expected all results to be utility calls")
		}
	}
}

func TestExecutionMetrics_PTYAdapter(t *testing.T) {
	s, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())

	m := &ExecutionMetrics{
		SessionID:      "sess-1",
		MessageID:      "msg-1",
		Provider:       "pty-claude",
		Adapter:        "pty",
		Model:          "claude-cli",
		DurationMs:     3000,
		ToolIterations: 0,
		ToolCalls:      0,
	}
	if err := s.RecordExecutionMetrics(context.Background(), m); err != nil {
		t.Fatalf("record: %v", err)
	}

	metrics, err := s.GetSessionExecutionMetrics(context.Background(), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 {
		t.Fatal("expected 1")
	}
	if metrics[0].Adapter != "pty" {
		t.Errorf("expected adapter=pty, got %s", metrics[0].Adapter)
	}
	if metrics[0].Provider != "pty-claude" {
		t.Errorf("expected provider=pty-claude, got %s", metrics[0].Provider)
	}
}
