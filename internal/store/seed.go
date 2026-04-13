package store

import (
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/pkg/models"
)

// Seed populates the database with initial data if the workspaces table is empty.
func (s *Store) Seed() error {
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM workspaces").Scan(&count); err != nil {
		return fmt.Errorf("check workspaces: %w", err)
	}
	if count > 0 {
		return nil // already seeded
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// --- Workspaces ---
	for _, w := range []struct {
		id, name, desc string
	}{
		{"default", "Default", "Default workspace"},
		{"personal", "Personal", "Personal planning and goals"},
	} {
		if _, err := tx.Exec(
			"INSERT INTO workspaces (id, name, description) VALUES (?, ?, ?)",
			w.id, w.name, w.desc,
		); err != nil {
			return fmt.Errorf("insert workspace %s: %w", w.id, err)
		}
	}

	// Agents are now file-based (internal/agent/). No agent seeding needed.
	// See internal/agent/builtin/default.md for the built-in default agent.

	// --- Provider: Anthropic ---
	providerID := "anthropic-001"
	if _, err := tx.Exec(
		`INSERT INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		providerID, "Anthropic", "anthropic", "",
	); err != nil {
		return fmt.Errorf("insert provider: %w", err)
	}

	// --- Model: Claude Sonnet 4 (default) ---
	// Pulled from the canonical registry so seed data can never drift from
	// the pricing/capabilities map consumed by usage.go, cost_monitor, etc.
	if def, ok := models.ByModelID(models.DefaultChatModelID); ok {
		if _, err := tx.Exec(
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
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		ptyProviderID, "Claude CLI (PTY)", "pty", "",
	); err != nil {
		return fmt.Errorf("insert pty provider: %w", err)
	}

	// --- Model: Claude CLI ---
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"claude-cli", ptyProviderID, "claude-cli", "Claude CLI",
		0, 0, true,
	); err != nil {
		return fmt.Errorf("insert claude-cli model: %w", err)
	}

	// --- Provider: PTY (Codex CLI) ---
	codexProviderID := "pty-codex-001"
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		codexProviderID, "Codex CLI (PTY)", "pty-codex", "",
	); err != nil {
		return fmt.Errorf("insert codex provider: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"codex-cli", codexProviderID, "codex-cli", "Codex CLI",
		0, 0, true,
	); err != nil {
		return fmt.Errorf("insert codex-cli model: %w", err)
	}

	// --- Provider: PTY (Gemini CLI) ---
	geminiProviderID := "pty-gemini-001"
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		geminiProviderID, "Gemini CLI (PTY)", "pty-gemini", "",
	); err != nil {
		return fmt.Errorf("insert gemini provider: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"gemini-cli", geminiProviderID, "gemini-cli", "Gemini CLI",
		0, 0, true,
	); err != nil {
		return fmt.Errorf("insert gemini-cli model: %w", err)
	}

	// --- Provider: PTY (Copilot CLI) ---
	copilotProviderID := "pty-copilot-001"
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		copilotProviderID, "GitHub Copilot CLI (PTY)", "pty-copilot", "",
	); err != nil {
		return fmt.Errorf("insert copilot provider: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"copilot-cli", copilotProviderID, "copilot-cli", "Copilot CLI",
		0, 0, false,
	); err != nil {
		return fmt.Errorf("insert copilot-cli model: %w", err)
	}

	// --- Provider: PTY (Aider CLI) ---
	aiderProviderID := "pty-aider-001"
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		aiderProviderID, "Aider CLI (PTY)", "pty-aider", "",
	); err != nil {
		return fmt.Errorf("insert aider provider: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"aider-cli", aiderProviderID, "aider-cli", "Aider CLI",
		0, 0, false,
	); err != nil {
		return fmt.Errorf("insert aider-cli model: %w", err)
	}

	// --- Provider: Google Gemini API ---
	geminiAPIProviderID := "gemini-api-001"
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		geminiAPIProviderID, "Google Gemini", "gemini", "",
	); err != nil {
		return fmt.Errorf("insert gemini api provider: %w", err)
	}

	for _, m := range []struct {
		id, modelID, display string
		ctx, maxOut          int
	}{
		{"gemini-2.5-flash", "gemini-2.5-flash", "Gemini 2.5 Flash", 1048576, 8192},
		{"gemini-2.5-pro", "gemini-2.5-pro", "Gemini 2.5 Pro", 1048576, 8192},
		{"gemini-2.0-flash", "gemini-2.0-flash", "Gemini 2.0 Flash", 1048576, 8192},
	} {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.id, geminiAPIProviderID, m.modelID, m.display, m.ctx, m.maxOut, true,
		); err != nil {
			return fmt.Errorf("insert gemini model %s: %w", m.id, err)
		}
	}

	// --- Provider: Mistral ---
	mistralProviderID := "mistral-001"
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		mistralProviderID, "Mistral", "mistral", "",
	); err != nil {
		return fmt.Errorf("insert mistral provider: %w", err)
	}

	for _, m := range []struct {
		id, modelID, display string
		ctx, maxOut          int
	}{
		{"mistral-large", "mistral-large-latest", "Mistral Large", 131072, 8192},
		{"mistral-medium", "mistral-medium-latest", "Mistral Medium", 131072, 8192},
		{"mistral-small", "mistral-small-latest", "Mistral Small", 131072, 8192},
		{"codestral", "codestral-latest", "Codestral", 262144, 8192},
	} {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.id, mistralProviderID, m.modelID, m.display, m.ctx, m.maxOut, true,
		); err != nil {
			return fmt.Errorf("insert mistral model %s: %w", m.id, err)
		}
	}

	// --- Provider: Azure OpenAI ---
	azureProviderID := "azure-openai-001"
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
		 VALUES (?, ?, ?, ?)`,
		azureProviderID, "Azure OpenAI", "azure-openai", "",
	); err != nil {
		return fmt.Errorf("insert azure-openai provider: %w", err)
	}

	// Azure models are deployment-specific; seed a placeholder.
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"azure-gpt4o", azureProviderID, "gpt-4o", "Azure GPT-4o",
		128000, 16384, true,
	); err != nil {
		return fmt.Errorf("insert azure gpt-4o model: %w", err)
	}

	// --- User settings singleton ---
	if _, err := tx.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		return fmt.Errorf("insert user_settings: %w", err)
	}

	// --- Catalog sources ---
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO catalog_sources (id, name, url, type, priority)
		 VALUES (?, ?, ?, ?, ?)`,
		"official", "Hollis Labs",
		"https://raw.githubusercontent.com/hollis-labs/plugin-catalog/main/catalog.yaml",
		"official", 100,
	); err != nil {
		return fmt.Errorf("insert catalog source: %w", err)
	}

	return tx.Commit()
}

// providerRow is the DB-layer tuple seeded into providers.
type providerRow struct {
	id, name, provType string
}

// seededProviders is the canonical provider catalog. Order matters: parent
// rows must insert before any model that references them.
var seededProviders = []providerRow{
	{"anthropic-001", "Anthropic", "anthropic"},
	{"openai-001", "OpenAI", "openai"},
	{"ollama-001", "Ollama", "ollama"},
	{"gemini-api-001", "Google Gemini", "gemini"},
	{"mistral-001", "Mistral", "mistral"},
	{"azure-openai-001", "Azure OpenAI", "azure-openai"},
	{"pty-001", "Claude CLI (PTY)", "pty"},
	{"pty-codex-001", "Codex CLI (PTY)", "pty-codex"},
	{"pty-gemini-001", "Gemini CLI (PTY)", "pty-gemini"},
	{"pty-copilot-001", "GitHub Copilot CLI (PTY)", "pty-copilot"},
	{"pty-aider-001", "Aider CLI (PTY)", "pty-aider"},
	{"openrouter-001", "OpenRouter", "openrouter"},
	{"openzen-001", "OpenZen", "openzen"},
	{"pty-junie-001", "Junie CLI (PTY)", "pty-junie"},
	{"pty-kiro-001", "Kiro CLI (PTY)", "pty-kiro"},
	{"pty-qwen-001", "Qwen CLI (PTY)", "pty-qwen"},
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
func (s *Store) SeedProviders() error {
	providers := seededProviders
	seeded := models.AllSeeded()

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, p := range providers {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO providers (id, name, provider_type, api_key) VALUES (?, ?, ?, ?)`,
			p.id, p.name, p.provType, "",
		); err != nil {
			return fmt.Errorf("upsert provider %s: %w", p.id, err)
		}
	}

	for _, m := range seeded {
		providerID := providerIDForType(m.Provider)
		if providerID == "" {
			return fmt.Errorf("seed: no provider row for provider_type %q (model %q)", m.Provider, m.ID)
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.ID, providerID, m.ModelID, m.DisplayName, m.ContextWindow, m.MaxOutput, m.Capabilities.SupportsToolCalling,
		); err != nil {
			return fmt.Errorf("upsert model %s: %w", m.ID, err)
		}
	}

	slog.Info("seed: upserted providers and models", "providers", len(providers), "models", len(seeded))
	return tx.Commit()
}

