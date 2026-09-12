package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/pkg/models"
)

// TestSyncModelsFromRegistry_UpsertsRealRows verifies the real DB-write sync
// path added for Phase 1 #06: after a simulated models.dev refresh
// (models.SyncFromCatalog) enriches the pkg/models overlay, calling
// SyncModelsFromRegistry must write those same enriched values into the
// `models` DB table, not just leave them sitting in the in-memory overlay.
func TestSyncModelsFromRegistry_UpsertsRealRows(t *testing.T) {
	s := newTestStore(t)

	// Mirrors real boot order (cmd/nanite/main.go: SeedProviders runs
	// before the container/model-catalog wiring): the providers row and
	// baseline model row must exist before an ongoing sync can upsert
	// against them.
	if err := s.SeedProviders(context.Background()); err != nil {
		t.Fatalf("SeedProviders: %v", err)
	}

	const wireModelID = "claude-sonnet-4-5-20250929"

	// Baseline: the seeded row should already exist carrying the STATIC
	// registry value, whatever that currently is.
	//
	// This used to hardcode 200000. That coupled this test to one model's
	// static number, so correcting that number to match models.dev broke a
	// test about something else — and worse, the corrected value happened to
	// equal the overlay value below, which would have made the whole test
	// vacuous while still passing if the constant had simply been bumped.
	//
	// The static values are now checked for correctness by
	// pkg/models.TestSeededValuesAgreeWithCatalog. This test owns a different
	// question — that a catalog refresh BEATS the static value — so it derives
	// the baseline and picks an overlay value guaranteed to differ from it.
	staticModel, ok := models.ByModelID(wireModelID)
	if !ok {
		t.Fatalf("%s is not in the static registry", wireModelID)
	}
	staticContext := staticModel.ContextWindow

	var baselineContext int
	if err := s.DB.QueryRow(
		`SELECT context_window FROM models WHERE model_id = ?`, wireModelID,
	).Scan(&baselineContext); err != nil {
		t.Fatalf("query baseline model row: %v", err)
	}
	if baselineContext != staticContext {
		t.Fatalf("baseline context_window = %d, want the static registry value %d",
			baselineContext, staticContext)
	}

	// Distinct from the static value by construction, so "the overlay won" is
	// observable rather than coincidental.
	overlayContext := staticContext + 111000
	overlayMaxOutput := staticModel.MaxOutput + 7000

	// Simulate a models.dev refresh that enriches this model beyond the
	// static registry defaults (context window bump, revised pricing) —
	// the same shape internal/service/container.go's syncCatalogToRegistry
	// builds from a live modelsdev.Client.List().
	models.SyncFromCatalog(models.CatalogInput{
		ContextWindows:  map[string]int{wireModelID: overlayContext},
		MaxOutputTokens: map[string]int{wireModelID: overlayMaxOutput},
		InputPricing:    map[string]float64{wireModelID: 2.5},
		OutputPricing:   map[string]float64{wireModelID: 12.0},
	})
	t.Cleanup(func() {
		// Package-level overlay is process-global; restore it to "no
		// catalog data" so later tests in this package see static
		// registry defaults again.
		models.SyncFromCatalog(models.CatalogInput{})
	})

	n, err := s.SyncModelsFromRegistry(context.Background())
	if err != nil {
		t.Fatalf("SyncModelsFromRegistry: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected at least one model synced, got 0")
	}

	var contextWindow, maxOutput int
	var pricingJSON string
	var isEnabled bool
	if err := s.DB.QueryRow(
		`SELECT context_window, max_output, pricing, is_enabled FROM models WHERE model_id = ?`,
		wireModelID,
	).Scan(&contextWindow, &maxOutput, &pricingJSON, &isEnabled); err != nil {
		t.Fatalf("query synced model row: %v", err)
	}

	if contextWindow != overlayContext {
		t.Errorf("context_window = %d, want %d (catalog-refreshed value, not the static %d)",
			contextWindow, overlayContext, staticContext)
	}
	if maxOutput != overlayMaxOutput {
		t.Errorf("max_output = %d, want %d (catalog-refreshed value)", maxOutput, overlayMaxOutput)
	}
	if !isEnabled {
		t.Errorf("is_enabled = false, want true")
	}

	var pricing map[string]float64
	if err := json.Unmarshal([]byte(pricingJSON), &pricing); err != nil {
		t.Fatalf("unmarshal pricing column %q: %v", pricingJSON, err)
	}
	if pricing["input_per_m"] != 2.5 {
		t.Errorf("pricing.input_per_m = %v, want 2.5", pricing["input_per_m"])
	}
	if pricing["output_per_m"] != 12.0 {
		t.Errorf("pricing.output_per_m = %v, want 12.0", pricing["output_per_m"])
	}

	// The row must still be the SAME row seed.go's one-time Seed()/
	// SeedProviders() created — same id, same provider_id — not a
	// duplicate inserted alongside it.
	var count int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM models WHERE model_id = ?`, wireModelID,
	).Scan(&count); err != nil {
		t.Fatalf("count rows for model_id: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 row for %s after sync, got %d (upsert should reconcile with the seeded row, not duplicate it)", wireModelID, count)
	}

	// Confirm no divergence between the DB row and the in-memory overlay
	// that fed it — the whole point of this sync path.
	overlay, ok := models.ByModelID(wireModelID)
	if !ok {
		t.Fatalf("models.ByModelID(%q) not found after SyncFromCatalog", wireModelID)
	}
	if overlay.ContextWindow != contextWindow {
		t.Errorf("overlay ContextWindow=%d disagrees with DB context_window=%d", overlay.ContextWindow, contextWindow)
	}
	if overlay.MaxOutput != maxOutput {
		t.Errorf("overlay MaxOutput=%d disagrees with DB max_output=%d", overlay.MaxOutput, maxOutput)
	}
}

// TestSyncModelsFromRegistry_Idempotent verifies repeated sync calls (as
// would happen on every models.dev refresh tick) don't create duplicate
// rows or error out.
func TestSyncModelsFromRegistry_Idempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.SeedProviders(context.Background()); err != nil {
		t.Fatalf("SeedProviders: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := s.SyncModelsFromRegistry(context.Background()); err != nil {
			t.Fatalf("SyncModelsFromRegistry call %d: %v", i, err)
		}
	}

	var totalModels, distinctKeys int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM models`).Scan(&totalModels); err != nil {
		t.Fatalf("count models: %v", err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(DISTINCT provider_id || ':' || model_id) FROM models`).Scan(&distinctKeys); err != nil {
		t.Fatalf("count distinct provider_id/model_id: %v", err)
	}
	if totalModels != distinctKeys {
		t.Errorf("expected every row to have a unique (provider_id, model_id) pair, got %d rows / %d distinct keys", totalModels, distinctKeys)
	}
}

// TestSyncModelsFromRegistry_SkipsUnknownProvider verifies models whose
// registry Provider has no seeded providers row (e.g. the PTY CLI
// entries — "pty", "pty-codex", etc. are never in seededProviders) are
// skipped rather than causing an FK-constraint error.
func TestSyncModelsFromRegistry_SkipsUnknownProvider(t *testing.T) {
	s := newTestStore(t)
	if err := s.SeedProviders(context.Background()); err != nil {
		t.Fatalf("SeedProviders: %v", err)
	}

	if _, err := s.SyncModelsFromRegistry(context.Background()); err != nil {
		t.Fatalf("SyncModelsFromRegistry: %v", err)
	}

	var count int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM models WHERE model_id = 'claude-cli'`,
	).Scan(&count); err != nil {
		t.Fatalf("count claude-cli rows: %v", err)
	}
	if count != 0 {
		t.Errorf("expected claude-cli to be skipped (provider %q not in seededProviders), got %d rows", "pty", count)
	}
}

// TestSyncModelsFromRegistry_ReconcilesRotatedModelID is a regression test
// for a real drift scenario found while verifying this sync path against a
// copy of a production database backup: a row seeded long ago under
// id="claude-sonnet" can carry a since-retired wire model_id (the registry
// comment on pkg/models.allModels notes claude-sonnet-4-20250514 was
// retired and replaced by claude-sonnet-4-5-20250929). Matching only on
// the natural (provider_id, model_id) unique key misses this case entirely
// — the stale row's model_id no longer matches the refreshed value, so the
// real collision lands on the `id` PRIMARY KEY, which
// ON CONFLICT(provider_id, model_id) does not catch. SyncModelsFromRegistry
// must update the existing row in place (same id, corrected model_id), not
// insert a duplicate and not error out.
func TestSyncModelsFromRegistry_ReconcilesRotatedModelID(t *testing.T) {
	s := newTestStore(t)
	if err := s.SeedProviders(context.Background()); err != nil {
		t.Fatalf("SeedProviders: %v", err)
	}

	// Simulate the stale production row directly: same id the static
	// registry still uses ("claude-sonnet"), but a retired wire model_id
	// that predates the current pkg/models entry.
	if _, err := s.DB.Exec(
		`UPDATE models SET model_id = 'claude-sonnet-4-20250514', display_name = 'Claude Sonnet 4'
		 WHERE id = 'claude-sonnet'`,
	); err != nil {
		t.Fatalf("simulate stale seeded row: %v", err)
	}

	var staleCount int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM models WHERE id = 'claude-sonnet' AND model_id = 'claude-sonnet-4-20250514'`,
	).Scan(&staleCount); err != nil || staleCount != 1 {
		t.Fatalf("failed to set up stale row fixture: count=%d err=%v", staleCount, err)
	}

	if _, err := s.SyncModelsFromRegistry(context.Background()); err != nil {
		t.Fatalf("SyncModelsFromRegistry: %v", err)
	}

	// The row must be corrected in place: same id, current model_id, and
	// exactly one row for that id — not a duplicate insert alongside the
	// stale one.
	var rowCount int
	var modelID string
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM models WHERE id = 'claude-sonnet'`,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows for id=claude-sonnet: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly 1 row for id=claude-sonnet after sync, got %d", rowCount)
	}
	if err := s.DB.QueryRow(
		`SELECT model_id FROM models WHERE id = 'claude-sonnet'`,
	).Scan(&modelID); err != nil {
		t.Fatalf("query model_id: %v", err)
	}
	if modelID != "claude-sonnet-4-5-20250929" {
		t.Errorf("model_id = %q, want %q (retired id should be corrected to the current registry value)", modelID, "claude-sonnet-4-5-20250929")
	}
}
