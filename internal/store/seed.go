package store

import (
	"fmt"
	"log"
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
		{"fragments-engine", "Fragments Engine", "AI-augmented software development portfolio"},
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

	// --- Model: Claude Sonnet 4 ---
	if _, err := tx.Exec(
		`INSERT INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"claude-sonnet", providerID, "claude-sonnet-4-20250514", "Claude Sonnet 4",
		200000, 16000, true,
	); err != nil {
		return fmt.Errorf("insert model: %w", err)
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

// SeedProviders upserts all known providers and models. Safe to call on every boot —
// uses INSERT OR IGNORE so existing rows are untouched.
func (s *Store) SeedProviders() error {
	type prov struct {
		id, name, provType string
	}
	type model struct {
		id, providerID, modelID, display string
		ctx, maxOut                       int
		tools                             bool
	}

	providers := []prov{
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

	models := []model{
		// Anthropic
		{"claude-sonnet", "anthropic-001", "claude-sonnet-4-20250514", "Claude Sonnet 4", 200000, 16000, true},
		{"claude-opus", "anthropic-001", "claude-opus-4-20250514", "Claude Opus 4", 200000, 32000, true},
		{"claude-haiku", "anthropic-001", "claude-haiku-4-5-20251001", "Claude Haiku 4.5", 200000, 8192, true},
		// OpenAI
		{"gpt-4o", "openai-001", "gpt-4o", "GPT-4o", 128000, 16384, true},
		{"gpt-4o-mini", "openai-001", "gpt-4o-mini", "GPT-4o Mini", 128000, 16384, true},
		{"o3", "openai-001", "o3", "o3", 200000, 100000, true},
		{"o4-mini", "openai-001", "o4-mini", "o4-mini", 200000, 100000, true},
		// Ollama (placeholder — user configures local models)
		{"ollama-llama3", "ollama-001", "llama3.1", "Llama 3.1", 131072, 4096, false},
		// Gemini API
		{"gemini-2.5-flash", "gemini-api-001", "gemini-2.5-flash", "Gemini 2.5 Flash", 1048576, 8192, true},
		{"gemini-2.5-pro", "gemini-api-001", "gemini-2.5-pro", "Gemini 2.5 Pro", 1048576, 8192, true},
		{"gemini-2.0-flash", "gemini-api-001", "gemini-2.0-flash", "Gemini 2.0 Flash", 1048576, 8192, true},
		// Mistral
		{"mistral-large", "mistral-001", "mistral-large-latest", "Mistral Large", 131072, 8192, true},
		{"mistral-medium", "mistral-001", "mistral-medium-latest", "Mistral Medium", 131072, 8192, true},
		{"mistral-small", "mistral-001", "mistral-small-latest", "Mistral Small", 131072, 8192, true},
		{"codestral", "mistral-001", "codestral-latest", "Codestral", 262144, 8192, true},
		// Azure OpenAI
		{"azure-gpt4o", "azure-openai-001", "gpt-4o", "Azure GPT-4o", 128000, 16384, true},
		// PTY CLIs
		{"claude-cli", "pty-001", "claude-cli", "Claude CLI", 0, 0, true},
		{"codex-cli", "pty-codex-001", "codex-cli", "Codex CLI", 0, 0, true},
		{"gemini-cli", "pty-gemini-001", "gemini-cli", "Gemini CLI", 0, 0, true},
		{"copilot-cli", "pty-copilot-001", "copilot-cli", "Copilot CLI", 0, 0, false},
		{"aider-cli", "pty-aider-001", "aider-cli", "Aider CLI", 0, 0, false},
		// CLI adapters (new)
		{"junie-cli", "pty-junie-001", "junie-cli", "Junie CLI", 0, 0, true},
		{"kiro-cli", "pty-kiro-001", "kiro-cli", "Kiro CLI", 0, 0, false},
		{"qwen-cli", "pty-qwen-001", "qwen-cli", "Qwen CLI", 0, 0, true},
		// OpenRouter (model gateway — uses provider-prefixed model IDs)
		{"or-claude-sonnet", "openrouter-001", "anthropic/claude-sonnet-4", "Claude Sonnet 4 (OR)", 200000, 16000, true},
		{"or-claude-opus", "openrouter-001", "anthropic/claude-opus-4", "Claude Opus 4 (OR)", 200000, 32000, true},
		{"or-gpt-4o", "openrouter-001", "openai/gpt-4o", "GPT-4o (OR)", 128000, 16384, true},
		{"or-gemini-2.5-flash", "openrouter-001", "google/gemini-2.5-flash", "Gemini 2.5 Flash (OR)", 1048576, 8192, true},
		{"or-llama-3.1-405b", "openrouter-001", "meta-llama/llama-3.1-405b-instruct", "Llama 3.1 405B (OR)", 131072, 4096, true},
		{"or-deepseek-r1", "openrouter-001", "deepseek/deepseek-r1", "DeepSeek R1 (OR)", 131072, 8192, true},
		// OpenZen (uses standard model IDs)
		{"oz-claude-sonnet", "openzen-001", "claude-sonnet-4-20250514", "Claude Sonnet 4 (OZ)", 200000, 16000, true},
		{"oz-claude-opus", "openzen-001", "claude-opus-4-20250514", "Claude Opus 4 (OZ)", 200000, 32000, true},
		{"oz-gpt-4o", "openzen-001", "gpt-4o", "GPT-4o (OZ)", 128000, 16384, true},
		{"oz-o3", "openzen-001", "o3", "o3 (OZ)", 200000, 100000, true},
	}

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

	for _, m := range models {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.id, m.providerID, m.modelID, m.display, m.ctx, m.maxOut, m.tools,
		); err != nil {
			return fmt.Errorf("upsert model %s: %w", m.id, err)
		}
	}

	log.Printf("seed: upserted %d providers and %d models", len(providers), len(models))
	return tx.Commit()
}

