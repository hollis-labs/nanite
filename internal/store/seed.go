package store

import (
	"fmt"

	"github.com/google/uuid"
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
		{"tiamat", "Project Tiamat", "AI-augmented software development portfolio"},
		{"personal", "Personal", "Personal planning and goals"},
	} {
		if _, err := tx.Exec(
			"INSERT INTO workspaces (id, name, description) VALUES (?, ?, ?)",
			w.id, w.name, w.desc,
		); err != nil {
			return fmt.Errorf("insert workspace %s: %w", w.id, err)
		}
	}

	// --- Agent profile: Mentat ---
	agentID := "mentat-001"
	systemPrompt := `You are Mentat, a cognitive AI partner. You help plan, organize, discuss, and manage work across multiple domains. You do NOT execute tasks directly — you think, plan, advise, and delegate to specialist agents when work needs to be done. You maintain context across conversations and help your human partner stay focused and effective.

Your strengths: strategic thinking, context management, task decomposition, cross-domain synthesis, and clear communication. You ask clarifying questions when needed and always think before acting.`

	if _, err := tx.Exec(
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		agentID, "Mentat", "mentat", systemPrompt,
		"Cognitive AI partner for planning and context management", false,
	); err != nil {
		return fmt.Errorf("insert agent profile: %w", err)
	}

	// --- Agent modes ---
	modes := []struct {
		slug, name, addendum string
	}{
		{"default", "Default",
			"You are in your default conversational mode. Be helpful, thoughtful, and proactive about managing context and suggesting next steps."},
		{"architect", "Architect",
			"You are in architect mode. Focus on system design, technical trade-offs, and architectural decisions. Ask probing questions about requirements, constraints, and failure modes. Suggest patterns and evaluate alternatives."},
		{"planner", "Planner",
			"You are in planner mode. Focus on breaking work into actionable tasks, estimating scope, identifying dependencies, and creating structured plans. Use numbered lists and clear acceptance criteria."},
		{"writer", "Writer",
			"You are in writer mode. Focus on prose quality, narrative structure, clarity, and voice. Help with drafting, editing, and refining written content. Be direct about what works and what doesn't."},
	}
	for _, m := range modes {
		if _, err := tx.Exec(
			`INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum)
			 VALUES (?, ?, ?, ?, ?)`,
			uuid.New().String(), agentID, m.slug, m.name, m.addendum,
		); err != nil {
			return fmt.Errorf("insert agent mode %s: %w", m.slug, err)
		}
	}

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

	return tx.Commit()
}
