package store

import (
	"errors"
	"reflect"
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
	if !errors.Is(err, ErrToolEnrichmentNotFound) {
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
	testNames := map[string]bool{"a_tool": true, "b_tool": true, "c_tool": true}
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

	var gotNames []string
	for _, r := range list {
		if testNames[r.ToolName] {
			gotNames = append(gotNames, r.ToolName)
		}
	}
	wantNames := []string{"c_tool", "b_tool", "a_tool"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("list order (filtered to test rows): got %v, want %v", gotNames, wantNames)
	}
}

// TestToolEnrichment_NanoPrecisionRoundTrip locks in the RFC3339Nano read/write
// contract: a record upserted with sub-second precision must round-trip through
// GetToolEnrichment and ListToolEnrichments without a parse failure. Guards
// against regression to RFC3339-only parsing that drops fractional seconds.
func TestToolEnrichment_NanoPrecisionRoundTrip(t *testing.T) {
	s := newTestStore(t)

	nanoTime := time.Date(2026, 4, 20, 12, 34, 56, 789_012_345, time.UTC)
	rec := ToolEnrichment{
		ToolName:  "nano_tool",
		HintsJSON: `{}`,
		UpdatedAt: nanoTime,
	}
	if err := s.UpsertToolEnrichment(rec); err != nil {
		t.Fatalf("UpsertToolEnrichment: %v", err)
	}

	got, err := s.GetToolEnrichment("nano_tool")
	if err != nil {
		t.Fatalf("GetToolEnrichment with nano-precision stamp failed: %v", err)
	}
	if !got.UpdatedAt.Equal(nanoTime) {
		t.Errorf("GetToolEnrichment lost precision: got %v, want %v", got.UpdatedAt, nanoTime)
	}

	list, err := s.ListToolEnrichments()
	if err != nil {
		t.Fatalf("ListToolEnrichments with nano-precision stamp failed: %v", err)
	}
	var found bool
	for _, r := range list {
		if r.ToolName == "nano_tool" {
			found = true
			if !r.UpdatedAt.Equal(nanoTime) {
				t.Errorf("ListToolEnrichments lost precision: got %v, want %v", r.UpdatedAt, nanoTime)
			}
		}
	}
	if !found {
		t.Fatal("nano_tool missing from ListToolEnrichments output")
	}
}

// TestToolEnrichment_ExternalWriterCompat simulates admin tooling / SQL scripts
// writing updated_at with RFC3339Nano-precision strings directly into the
// tool_enrichments table (bypassing UpsertToolEnrichment's Go formatting).
// Reads must not fail on the higher-precision stamp.
func TestToolEnrichment_ExternalWriterCompat(t *testing.T) {
	s := newTestStore(t)

	_, err := s.DB.Exec(
		`INSERT INTO tool_enrichments (tool_name, hints_json, updated_at) VALUES (?, ?, ?)`,
		"external_tool", `{}`, "2026-04-20T12:34:56.123456789Z",
	)
	if err != nil {
		t.Fatalf("direct INSERT: %v", err)
	}

	got, err := s.GetToolEnrichment("external_tool")
	if err != nil {
		t.Fatalf("GetToolEnrichment rejected RFC3339Nano string: %v", err)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("GetToolEnrichment returned zero UpdatedAt for RFC3339Nano input")
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
	if _, err := s.GetToolEnrichment("del_tool"); !errors.Is(err, ErrToolEnrichmentNotFound) {
		t.Errorf("expected not-found after delete, got %v", err)
	}
}
