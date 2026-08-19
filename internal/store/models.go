package store

import (
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/pkg/models"
)

// SyncModelsFromRegistry upserts models table rows for every chat model in
// pkg/models' curated seed set (models.AllSeeded — the same universe
// Seed()/SeedProviders() already write into this table), reading each
// model's *current* values through models.ByModelID.
//
// ByModelID checks the models.dev catalog overlay (populated by
// models.SyncFromCatalog) before falling back to the static registry, so
// as long as the caller invokes this after SyncFromCatalog has run for the
// current refresh cycle, the DB row and the in-memory overlay are always
// reading the exact same merged value — there is exactly one merge
// (registry.go's SyncFromCatalog), and this just materialises its result
// into the DB. That is the mechanism that keeps the two from diverging;
// this function must not re-derive context window / max output / pricing
// by any other path.
//
// Matching key: pkg/models.Model.ID (the models table's real PRIMARY KEY),
// not (provider_id, model_id) alone. Verified against a real copy of a
// production database (not just an empty fixture) that a row seeded long
// ago under id="claude-sonnet" can carry a since-retired wire model_id
// (e.g. "claude-sonnet-4-20250514", replaced by
// "claude-sonnet-4-5-20250929" per registry.go's own retirement note) —
// exactly the kind of drift this task exists to fix going forward. Model
// ID rotation while the internal id stays fixed is the reason pkg/models
// keeps ID and ModelID as separate fields in the first place (see the
// doc comments on Model.ID / Model.ModelID); an upsert keyed only on the
// natural (provider_id, model_id) unique index would insert a *second*
// row instead of correcting the stale one, because the stale row's
// model_id no longer matches the refreshed value and the collision it
// actually hits is on the `id` primary key, which ON CONFLICT(provider_id,
// model_id) does not catch.
//
// Because of that, this does an UPDATE-by-id first; only when no row with
// that id exists yet does it fall back to INSERT ... ON CONFLICT
// (provider_id, model_id) DO UPDATE, which reconciles with a row seeded
// under a different id for the same provider+model pair (not expected
// for any model in the curated set today, but a defensive fallback
// instead of a silent duplicate).
//
// Deliberately scoped to models.AllSeeded()'s curated set rather than
// every model models.dev's catalog returns for a known provider — that
// upstream catalog carries dozens of speculative/legacy/dated model IDs
// per provider that were never part of Nanite's own seeded/curated list.
// Writing all of them into `models` would flood the composer's model
// dropdown (GET /api/models -> ListModels) with rows the runtime doesn't
// otherwise know about. Refreshing metadata for the models we already
// seed is what "models.dev refresh" is for here; importing its full
// long-tail catalog is a different, unrequested feature.
//
// Models whose registry Provider has no matching seededProviders row are
// skipped (mirrors SeedProviders' skip-and-log behavior for the same
// reason: a model catalog can outrun the provider rows we seed).
func (s *Store) SyncModelsFromRegistry() (int, error) {
	seeded := models.AllSeeded()
	if len(seeded) == 0 {
		return 0, nil
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	applied := 0
	for _, m := range seeded {
		// Re-resolve through ByModelID so overlay values (context window,
		// max output, pricing refreshed from models.dev) win over the
		// static AllSeeded() snapshot when the overlay has this model.
		current, ok := models.ByModelID(m.ModelID)
		if !ok {
			current = m
		}
		providerID := providerIDForType(current.Provider)
		if providerID == "" {
			continue
		}
		pricing, err := json.Marshal(map[string]float64{
			"input_per_m":  current.InputPricePerM,
			"output_per_m": current.OutputPricePerM,
		})
		if err != nil {
			return applied, fmt.Errorf("marshal pricing for %s: %w", current.ModelID, err)
		}

		res, err := tx.Exec(
			`UPDATE models SET
			     provider_id     = ?,
			     model_id        = ?,
			     display_name    = ?,
			     context_window  = ?,
			     max_output      = ?,
			     supports_tools  = ?,
			     supports_vision = ?,
			     pricing         = ?,
			     is_enabled      = TRUE
			 WHERE id = ?`,
			providerID, current.ModelID, current.DisplayName,
			current.ContextWindow, current.MaxOutput,
			current.Capabilities.SupportsToolCalling, current.Capabilities.SupportsVision,
			string(pricing), current.ID,
		)
		if err != nil {
			return applied, fmt.Errorf("update model %s (id=%s): %w", current.ModelID, current.ID, err)
		}
		if rows, rerr := res.RowsAffected(); rerr == nil && rows > 0 {
			applied++
			continue
		}

		// No row keyed by this stable internal id exists yet — insert
		// fresh, reconciling via the natural (provider_id, model_id) key
		// with any row a different code path already created for the
		// same provider+model pair rather than duplicating it.
		if _, err := tx.Exec(
			`INSERT INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools, supports_vision, pricing, is_enabled)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE)
			 ON CONFLICT(provider_id, model_id) DO UPDATE SET
			     display_name    = excluded.display_name,
			     context_window  = excluded.context_window,
			     max_output      = excluded.max_output,
			     supports_tools  = excluded.supports_tools,
			     supports_vision = excluded.supports_vision,
			     pricing         = excluded.pricing,
			     is_enabled      = TRUE`,
			current.ID, providerID, current.ModelID, current.DisplayName,
			current.ContextWindow, current.MaxOutput,
			current.Capabilities.SupportsToolCalling, current.Capabilities.SupportsVision,
			string(pricing),
		); err != nil {
			return applied, fmt.Errorf("insert model %s (id=%s): %w", current.ModelID, current.ID, err)
		}
		applied++
	}

	if err := tx.Commit(); err != nil {
		return applied, fmt.Errorf("commit: %w", err)
	}
	return applied, nil
}
