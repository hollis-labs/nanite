package store

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/store/seedcatalog"
	"github.com/hollis-labs/nanite/pkg/models"
)

// Seed populates the database with initial data if it hasn't been seeded yet.
//
// Phase 0 item 20 (retire workspaces,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md): the
// idempotency gate used to be "is the workspaces table empty" — that
// table is retired in full. user_settings is a genuine singleton (its
// only row, id=1, is inserted unconditionally at the end of this same
// seed transaction below), so its presence is the new gate.
func (s *Store) Seed(ctx context.Context) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_settings").Scan(&count); err != nil {
		return fmt.Errorf("check user_settings: %w", err)
	}
	if count > 0 {
		return nil // already seeded
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	// Agents are now file-based (internal/agent/). No agent seeding needed.
	// See internal/agent/builtin/default.md for the built-in default agent.

	// --- Provider: Anthropic ---
	// default_model is sourced from seedcatalog so the canonical seed value
	// for "anthropic" lives in exactly one place; runtime resolution reads
	// from the providers row, not from a Go literal (CW-20260526-0003).
	providerID := "anthropic-001"
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO providers (id, name, provider_type, default_model)
		 VALUES (?, ?, ?, ?)`,
		providerID, "Anthropic", seedcatalog.DefaultProviderType,
		seedcatalog.ProviderDefaultModels[seedcatalog.DefaultProviderType],
	); err != nil {
		return fmt.Errorf("insert provider: %w", err)
	}

	// --- Model: Claude Sonnet 4.5 (default) ---
	// Pulled from the canonical registry so seed data can never drift from
	// the pricing/capabilities map consumed by usage.go, cost_monitor, etc.
	if def, ok := models.ByModelID(seedcatalog.DefaultChatModelID); ok {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			def.ID, providerID, def.ModelID, def.DisplayName,
			def.ContextWindow, def.MaxOutput, def.Capabilities.SupportsToolCalling,
		); err != nil {
			return fmt.Errorf("insert model: %w", err)
		}
	}

	// --- Provider: PTY (Claude CLI) ---
	// Use INSERT OR IGNORE — migrations 010/011 may have already created these rows.
	ptyProviderID := "pty-001"
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO providers (id, name, provider_type)
		 VALUES (?, ?, ?)`,
		ptyProviderID, "Claude CLI (PTY)", "pty",
	); err != nil {
		return fmt.Errorf("insert pty provider: %w", err)
	}

	// --- Model: Claude CLI ---
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"claude-cli", ptyProviderID, "claude-cli", "Claude CLI",
		0, 0, true,
	); err != nil {
		return fmt.Errorf("insert claude-cli model: %w", err)
	}

	// --- Provider: PTY (Codex CLI) ---
	codexProviderID := "pty-codex-001"
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO providers (id, name, provider_type)
		 VALUES (?, ?, ?)`,
		codexProviderID, "Codex CLI (PTY)", "pty-codex",
	); err != nil {
		return fmt.Errorf("insert codex provider: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"codex-cli", codexProviderID, "codex-cli", "Codex CLI",
		0, 0, true,
	); err != nil {
		return fmt.Errorf("insert codex-cli model: %w", err)
	}

	// --- Provider: PTY (Gemini CLI) ---
	geminiProviderID := "pty-gemini-001"
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO providers (id, name, provider_type)
		 VALUES (?, ?, ?)`,
		geminiProviderID, "Gemini CLI (PTY)", "pty-gemini",
	); err != nil {
		return fmt.Errorf("insert gemini provider: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"gemini-cli", geminiProviderID, "gemini-cli", "Gemini CLI",
		0, 0, true,
	); err != nil {
		return fmt.Errorf("insert gemini-cli model: %w", err)
	}

	// --- Provider: PTY (Copilot CLI) ---
	copilotProviderID := "pty-copilot-001"
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO providers (id, name, provider_type)
		 VALUES (?, ?, ?)`,
		copilotProviderID, "GitHub Copilot CLI (PTY)", "pty-copilot",
	); err != nil {
		return fmt.Errorf("insert copilot provider: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"copilot-cli", copilotProviderID, "copilot-cli", "Copilot CLI",
		0, 0, false,
	); err != nil {
		return fmt.Errorf("insert copilot-cli model: %w", err)
	}

	// --- Provider: PTY (Aider CLI) ---
	aiderProviderID := "pty-aider-001"
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO providers (id, name, provider_type)
		 VALUES (?, ?, ?)`,
		aiderProviderID, "Aider CLI (PTY)", "pty-aider",
	); err != nil {
		return fmt.Errorf("insert aider provider: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"aider-cli", aiderProviderID, "aider-cli", "Aider CLI",
		0, 0, false,
	); err != nil {
		return fmt.Errorf("insert aider-cli model: %w", err)
	}

	// Removed Step 6.5 follow-up (SP-20260508-0001): Google Gemini API,
	// Mistral, and Azure OpenAI provider/model seed blocks. Their runtime
	// constructors were deleted in commit 376390c — these rows were inert
	// at runtime but surfaced in the providers UI dropdown. Existing
	// deployments retain stale rows; operator cleanup is tracked at
	// followups.nanite.cw_20260508_0010.seed_registry_dead_cli_entries.

	// --- User settings singleton ---
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		return fmt.Errorf("insert user_settings: %w", err)
	}

	return tx.Commit()
}

// providerRow is the DB-layer tuple seeded into providers.
type providerRow struct {
	id, name, provType string
}

// seededProviders is the canonical provider catalog. Order matters: parent
// rows must insert before any model that references them.
//
// Step 6.5 (SP-20260508-0001) reduced the API-provider catalog to
// Anthropic + OpenAI; the gemini/mistral/azure-openai/openrouter/openzen/
// ollama API rows have been removed. CLI launches are surfaced through
// boot-profile harness entries, not raw PTY provider rows.
var seededProviders = []providerRow{
	{"anthropic-001", "Anthropic", "anthropic"},
	{"openai-001", "OpenAI", "openai"},
}

// providerIDForType resolves a registry provider_type to the DB row id. Used
// when materialising pkg/models rows into the models table.
func providerIDForType(t string) string {
	for _, p := range seededProviders {
		if p.provType == t {
			return p.id
		}
	}
	return ""
}

// SeedProviders upserts all known providers and models. Safe to call on every
// boot — uses INSERT OR IGNORE so existing rows are untouched. The model
// catalog is pulled from pkg/models.AllSeeded() so pricing and capability
// updates live in exactly one place.
func (s *Store) SeedProviders(ctx context.Context) error {
	providers := seededProviders
	seeded := models.AllSeeded()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	for _, p := range providers {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO providers (id, name, provider_type, default_model)
			 VALUES (?, ?, ?, ?)`,
			p.id, p.name, p.provType, seedcatalog.ProviderDefaultModels[p.provType],
		); err != nil {
			return fmt.Errorf("upsert provider %s: %w", p.id, err)
		}
		// Backfill default_model for providers seeded before the column
		// existed (CW-20260526-0003 migration 086 added it). The UPDATE
		// only runs when the cell is empty AND we know a seed value for
		// the provider_type — operator overrides are preserved.
		if def, ok := seedcatalog.ProviderDefaultModels[p.provType]; ok {
			if _, err := tx.ExecContext(ctx,
				`UPDATE providers SET default_model = ?
				 WHERE id = ? AND COALESCE(default_model, '') = ''`,
				def, p.id,
			); err != nil {
				return fmt.Errorf("backfill default_model for %s: %w", p.id, err)
			}
		}
	}

	modelsSeeded := 0
	skippedByProvider := map[string]int{}
	for _, m := range seeded {
		providerID := providerIDForType(m.Provider)
		if providerID == "" {
			// Catalog has a model whose provider isn't in our seeded set
			// (e.g. an upstream catalog version added gemini before the seed
			// caught up). Skip silently per-model but surface once per
			// provider so config drift is visible in logs.
			skippedByProvider[m.Provider]++
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.ID, providerID, m.ModelID, m.DisplayName, m.ContextWindow, m.MaxOutput, m.Capabilities.SupportsToolCalling,
		); err != nil {
			return fmt.Errorf("upsert model %s: %w", m.ID, err)
		}
		modelsSeeded++
	}
	for provType, count := range skippedByProvider {
		slog.Warn("seed: skipped models with unseeded provider_type", "provider_type", provType, "count", count)
	}

	slog.Info("seed: upserted providers and models", "providers", len(providers), "models", modelsSeeded)
	return tx.Commit()
}
