package store

import (
	"context"
	"testing"
	"time"
)

func TestWriteCompactionEvent(t *testing.T) {
	s := newTestStore(t)
	defer s.DB.Close()

	evt := CompactionEvent{
		ID:                   "test-evt-1",
		SessionID:            "session-1",
		SummaryMode:          "code",
		SummaryTokenCount:    150,
		OriginalTokenCount:   500,
		EvictedCachePointers: []string{"ptr-1", "ptr-2"},
		PreservedSources:     []string{"src-1"},
		StagesApplied:        []string{"drop_enrichment", "summarize_oldest"},
		CreatedAt:            time.Now().UTC().Format(time.RFC3339),
	}

	err := s.WriteCompactionEvent(context.Background(), evt)
	if err != nil {
		t.Fatalf("WriteCompactionEvent failed: %v", err)
	}

	// Verify written record
	rows, err := s.DB.Query(
		`SELECT id, session_id, summary_mode, summary_token_count, original_token_count
		 FROM compaction_events WHERE id = ?`, "test-evt-1")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("event not found in database")
	}

	var id, sid, mode string
	var sumTokens, origTokens int
	if err := rows.Scan(&id, &sid, &mode, &sumTokens, &origTokens); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if id != "test-evt-1" || sid != "session-1" || mode != "code" {
		t.Errorf("unexpected values: id=%s sid=%s mode=%s", id, sid, mode)
	}
	if sumTokens != 150 || origTokens != 500 {
		t.Errorf("unexpected tokens: sumTokens=%d origTokens=%d", sumTokens, origTokens)
	}
}

func TestGetLatestCompactionEvent(t *testing.T) {
	s := newTestStore(t)
	defer s.DB.Close()

	sessionID := "session-2"
	now := time.Now().UTC()

	// Write two events
	evt1 := CompactionEvent{
		ID:                   "evt-old",
		SessionID:            sessionID,
		SummaryMode:          "plan",
		EvictedCachePointers: []string{},
		PreservedSources:     []string{},
		StagesApplied:        []string{},
		CreatedAt:            now.Add(-1 * time.Minute).Format(time.RFC3339),
	}
	evt2 := CompactionEvent{
		ID:                   "evt-new",
		SessionID:            sessionID,
		SummaryMode:          "research",
		EvictedCachePointers: []string{},
		PreservedSources:     []string{},
		StagesApplied:        []string{},
		CreatedAt:            now.Format(time.RFC3339),
	}

	if err := s.WriteCompactionEvent(context.Background(), evt1); err != nil {
		t.Fatalf("WriteCompactionEvent failed: %v", err)
	}
	if err := s.WriteCompactionEvent(context.Background(), evt2); err != nil {
		t.Fatalf("WriteCompactionEvent failed: %v", err)
	}

	// Get latest
	latest, err := s.GetLatestCompactionEvent(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetLatestCompactionEvent failed: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil event")
	}

	if latest.ID != "evt-new" {
		t.Errorf("expected latest event id='evt-new', got %s", latest.ID)
	}
	if latest.SummaryMode != "research" {
		t.Errorf("expected mode='research', got %s", latest.SummaryMode)
	}
}

func TestGetLatestCompactionEvent_notFound(t *testing.T) {
	s := newTestStore(t)
	defer s.DB.Close()

	evt, err := s.GetLatestCompactionEvent(context.Background(), "nonexistent-session")
	if err != nil {
		t.Fatalf("GetLatestCompactionEvent failed: %v", err)
	}
	if evt != nil {
		t.Fatal("expected nil event for nonexistent session")
	}
}

func TestListCompactionEventsBySession(t *testing.T) {
	s := newTestStore(t)
	defer s.DB.Close()

	sessionID := "session-3"
	now := time.Now().UTC()

	// Write 3 events
	for i := 0; i < 3; i++ {
		evt := CompactionEvent{
			ID:                   "evt-" + string(rune(i+'0')),
			SessionID:            sessionID,
			SummaryMode:          "general",
			EvictedCachePointers: []string{},
			PreservedSources:     []string{},
			StagesApplied:        []string{"summarize_oldest"},
			CreatedAt:            now.Add(-1 * time.Duration(2-i) * time.Minute).Format(time.RFC3339),
		}
		if err := s.WriteCompactionEvent(context.Background(), evt); err != nil {
			t.Fatalf("WriteCompactionEvent failed: %v", err)
		}
	}

	// List with limit
	events, err := s.ListCompactionEventsBySession(context.Background(), sessionID, 10)
	if err != nil {
		t.Fatalf("ListCompactionEventsBySession failed: %v", err)
	}

	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}

	// Check ordering (newest first)
	if events[0].ID != "evt-2" || events[1].ID != "evt-1" || events[2].ID != "evt-0" {
		t.Errorf("unexpected event order: %v", []string{events[0].ID, events[1].ID, events[2].ID})
	}
}

func TestCompactionEvent_JSONArrayHandling(t *testing.T) {
	s := newTestStore(t)
	defer s.DB.Close()

	evt := CompactionEvent{
		ID:                   "evt-json",
		SessionID:            "session-4",
		SummaryMode:          "code",
		EvictedCachePointers: []string{"ptr-a", "ptr-b"},
		PreservedSources:     []string{"src-x", "src-y", "src-z"},
		StagesApplied:        []string{"drop_enrichment", "dedupe_tool_results", "summarize_oldest"},
		CreatedAt:            time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.WriteCompactionEvent(context.Background(), evt); err != nil {
		t.Fatalf("WriteCompactionEvent failed: %v", err)
	}

	retrieved, err := s.GetLatestCompactionEvent(context.Background(), "session-4")
	if err != nil {
		t.Fatalf("GetLatestCompactionEvent failed: %v", err)
	}

	if len(retrieved.EvictedCachePointers) != 2 {
		t.Errorf("expected 2 evicted pointers, got %d", len(retrieved.EvictedCachePointers))
	}
	if len(retrieved.PreservedSources) != 3 {
		t.Errorf("expected 3 preserved sources, got %d", len(retrieved.PreservedSources))
	}
	if len(retrieved.StagesApplied) != 3 {
		t.Errorf("expected 3 stages, got %d", len(retrieved.StagesApplied))
	}

	// Verify values
	if retrieved.EvictedCachePointers[0] != "ptr-a" || retrieved.EvictedCachePointers[1] != "ptr-b" {
		t.Errorf("unexpected evicted pointers: %v", retrieved.EvictedCachePointers)
	}
}

func TestCompactionEvent_NullableFields(t *testing.T) {
	s := newTestStore(t)
	defer s.DB.Close()

	stashID := "stash-123"
	start := "turn-10"
	end := "turn-50"

	evt := CompactionEvent{
		ID:                   "evt-nullable",
		SessionID:            "session-5",
		SummaryMode:          "plan",
		CoverageWindowStart:  &start,
		CoverageWindowEnd:    &end,
		HandoffStashID:       &stashID,
		EvictedCachePointers: []string{},
		PreservedSources:     []string{},
		StagesApplied:        []string{},
		CreatedAt:            time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.WriteCompactionEvent(context.Background(), evt); err != nil {
		t.Fatalf("WriteCompactionEvent failed: %v", err)
	}

	retrieved, err := s.GetLatestCompactionEvent(context.Background(), "session-5")
	if err != nil {
		t.Fatalf("GetLatestCompactionEvent failed: %v", err)
	}

	if retrieved.CoverageWindowStart == nil || *retrieved.CoverageWindowStart != "turn-10" {
		t.Errorf("unexpected coverage_window_start: %v", retrieved.CoverageWindowStart)
	}
	if retrieved.CoverageWindowEnd == nil || *retrieved.CoverageWindowEnd != "turn-50" {
		t.Errorf("unexpected coverage_window_end: %v", retrieved.CoverageWindowEnd)
	}
	if retrieved.HandoffStashID == nil || *retrieved.HandoffStashID != "stash-123" {
		t.Errorf("unexpected handoff_stash_id: %v", retrieved.HandoffStashID)
	}
}
