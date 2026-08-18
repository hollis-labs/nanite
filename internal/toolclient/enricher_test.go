package toolclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/go-toolbroker/broker"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

func newTestStoreForEnricher(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreEnricher_LookupExisting(t *testing.T) {
	s := newTestStoreForEnricher(t)

	h := broker.Hints{OutputShape: "array of strings"}
	hj, err := broker.MarshalHints(h)
	if err != nil {
		t.Fatalf("MarshalHints: %v", err)
	}
	// UpsertToolEnrichment was cut in 18a-cut-dead-storage-and-config (zero
	// callers anywhere in the app); insert the row directly so this test can
	// still exercise the live GetToolEnrichment/NewStoreEnricher read path.
	if _, err := s.DB.Exec(
		`INSERT INTO tool_enrichments (tool_name, hints_json, updated_at) VALUES (?, ?, ?)`,
		"example_tool", hj, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert tool_enrichments: %v", err)
	}

	enr := toolclient.NewStoreEnricher(s)
	got, ok, err := enr.LookupByToolName(context.Background(), "example_tool")
	if err != nil {
		t.Fatalf("LookupByToolName: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for existing tool")
	}
	if got.OutputShape != "array of strings" {
		t.Errorf("OutputShape: got %q, want %q", got.OutputShape, "array of strings")
	}
}

func TestStoreEnricher_LookupMissing(t *testing.T) {
	s := newTestStoreForEnricher(t)
	enr := toolclient.NewStoreEnricher(s)

	_, ok, err := enr.LookupByToolName(context.Background(), "no_such_tool")
	if err != nil {
		t.Fatalf("unexpected error on missing tool: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing tool")
	}
}

func TestStoreEnricher_LookupNilStore(t *testing.T) {
	// Nil-safe enricher: returns ok=false without error. Lets toolclient
	// construction sites that don't wire a store (tests, CLI) still satisfy
	// broker.Enricher without branching.
	var enr broker.Enricher = toolclient.NewStoreEnricher(nil)
	_, ok, err := enr.LookupByToolName(context.Background(), "anything")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false when store is nil")
	}
}
