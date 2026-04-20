package store

import (
	"testing"
	"time"
)

func TestToolEnrichment_UpsertAndGet(t *testing.T) {
	s := newTestStore(t)

	rec := ToolEnrichment{
		ToolName:  "test_tool",
		HintsJSON: `{"output_shape":"Returns a thing"}`,
		UpdatedAt: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
	}
	if err := s.UpsertToolEnrichment(rec); err != nil {
		t.Fatalf("UpsertToolEnrichment: %v", err)
	}

	got, err := s.GetToolEnrichment("test_tool")
	if err != nil {
		t.Fatalf("GetToolEnrichment: %v", err)
	}
	if got.HintsJSON != rec.HintsJSON {
		t.Errorf("HintsJSON: got %q, want %q", got.HintsJSON, rec.HintsJSON)
	}
	if !got.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want %v", got.UpdatedAt, rec.UpdatedAt)
	}
}

func TestToolEnrichment_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetToolEnrichment("nonexistent_tool")
	if err != ErrToolEnrichmentNotFound {
		t.Errorf("expected ErrToolEnrichmentNotFound, got %v", err)
	}
}

func TestToolEnrichment_UpsertReplaces(t *testing.T) {
	s := newTestStore(t)

	first := ToolEnrichment{
		ToolName:  "repl_tool",
		HintsJSON: `{"v":1}`,
		UpdatedAt: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
	}
	if err := s.UpsertToolEnrichment(first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := ToolEnrichment{
		ToolName:  "repl_tool",
		HintsJSON: `{"v":2}`,
		UpdatedAt: time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC),
	}
	if err := s.UpsertToolEnrichment(second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := s.GetToolEnrichment("repl_tool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.HintsJSON != `{"v":2}` {
		t.Errorf("expected replaced row, got %q", got.HintsJSON)
	}
}

func TestToolEnrichment_List(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	for i, name := range []string{"a_tool", "b_tool", "c_tool"} {
		rec := ToolEnrichment{
			ToolName:  name,
			HintsJSON: `{}`,
			UpdatedAt: now.Add(time.Duration(i) * time.Hour),
		}
		if err := s.UpsertToolEnrichment(rec); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}

	list, err := s.ListToolEnrichments()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Seed row from migration (clockwork_task_list) adds +1 to the count.
	if len(list) < 3 {
		t.Errorf("expected >= 3 records, got %d", len(list))
	}
}

func TestToolEnrichment_Delete(t *testing.T) {
	s := newTestStore(t)
	rec := ToolEnrichment{
		ToolName:  "del_tool",
		HintsJSON: `{}`,
		UpdatedAt: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
	}
	if err := s.UpsertToolEnrichment(rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.DeleteToolEnrichment("del_tool"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetToolEnrichment("del_tool"); err != ErrToolEnrichmentNotFound {
		t.Errorf("expected not-found after delete, got %v", err)
	}
}
