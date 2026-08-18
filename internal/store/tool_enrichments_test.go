package store

import (
	"errors"
	"testing"
	"time"
)

func TestToolEnrichment_Get(t *testing.T) {
	s := newTestStore(t)

	want := ToolEnrichment{
		ToolName:  "test_tool",
		HintsJSON: `{"output_shape":"Returns a thing"}`,
		UpdatedAt: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
	}
	if _, err := s.DB.Exec(
		`INSERT INTO tool_enrichments (tool_name, hints_json, updated_at) VALUES (?, ?, ?)`,
		want.ToolName, want.HintsJSON, want.UpdatedAt.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert tool_enrichments: %v", err)
	}

	got, err := s.GetToolEnrichment("test_tool")
	if err != nil {
		t.Fatalf("GetToolEnrichment: %v", err)
	}
	if got.HintsJSON != want.HintsJSON {
		t.Errorf("HintsJSON: got %q, want %q", got.HintsJSON, want.HintsJSON)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
}

func TestToolEnrichment_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetToolEnrichment("nonexistent_tool")
	if !errors.Is(err, ErrToolEnrichmentNotFound) {
		t.Errorf("expected ErrToolEnrichmentNotFound, got %v", err)
	}
}

// TestToolEnrichment_NanoPrecisionRoundTrip locks in the RFC3339Nano read
// contract: a record written with sub-second precision must round-trip
// through GetToolEnrichment without a parse failure. Guards against
// regression to RFC3339-only parsing that drops fractional seconds.
func TestToolEnrichment_NanoPrecisionRoundTrip(t *testing.T) {
	s := newTestStore(t)

	nanoTime := time.Date(2026, 4, 20, 12, 34, 56, 789_012_345, time.UTC)
	if _, err := s.DB.Exec(
		`INSERT INTO tool_enrichments (tool_name, hints_json, updated_at) VALUES (?, ?, ?)`,
		"nano_tool", `{}`, nanoTime.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert tool_enrichments: %v", err)
	}

	got, err := s.GetToolEnrichment("nano_tool")
	if err != nil {
		t.Fatalf("GetToolEnrichment with nano-precision stamp failed: %v", err)
	}
	if !got.UpdatedAt.Equal(nanoTime) {
		t.Errorf("GetToolEnrichment lost precision: got %v, want %v", got.UpdatedAt, nanoTime)
	}
}

// TestToolEnrichment_ExternalWriterCompat simulates admin tooling / SQL scripts
// writing updated_at with RFC3339Nano-precision strings directly into the
// tool_enrichments table (there is no in-app writer anymore — the write-side
// CRUD was cut in 18a-cut-dead-storage-and-config as dead code). Reads must
// not fail on the higher-precision stamp.
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
