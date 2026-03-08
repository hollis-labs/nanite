package store

import (
	"fmt"
	"log"

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
	mentatPrompt := `You are Mentat, a cognitive AI partner. You help plan, organize, discuss, and manage work across multiple domains. You do NOT execute tasks directly — you think, plan, advise, and delegate to specialist agents when work needs to be done. You maintain context across conversations and help your human partner stay focused and effective.

Your strengths: strategic thinking, context management, task decomposition, cross-domain synthesis, and clear communication. You ask clarifying questions when needed and always think before acting.`

	if _, err := tx.Exec(
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"mentat-001", "Mentat", "mentat", mentatPrompt,
		"Cognitive AI partner for planning and context management", false,
	); err != nil {
		return fmt.Errorf("insert mentat profile: %w", err)
	}

	// Mentat modes
	mentatModes := []struct {
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
	for _, m := range mentatModes {
		if _, err := tx.Exec(
			`INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum)
			 VALUES (?, ?, ?, ?, ?)`,
			uuid.New().String(), "mentat-001", m.slug, m.name, m.addendum,
		); err != nil {
			return fmt.Errorf("insert mentat mode %s: %w", m.slug, err)
		}
	}

	// --- Agent profile: Developer ---
	devPrompt := `You are Developer, a hands-on software engineering agent. You write, review, and debug code across multiple languages and frameworks. You have access to development tools (read, write, edit, grep, glob, bash) and can execute commands directly. You follow best practices, write clean code, and explain your reasoning.`

	if _, err := tx.Exec(
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute,
		        mcp_servers, tool_permissions)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"developer-001", "Developer", "developer", devPrompt,
		"Hands-on software engineering agent with dev tools", true,
		`["dev"]`, `{"allow_list":["mcp__dev__*"]}`,
	); err != nil {
		return fmt.Errorf("insert developer profile: %w", err)
	}

	// Developer modes
	devModes := []struct {
		slug, name, addendum, toolOverrides string
	}{
		{"default", "Default",
			"You are in your default development mode. Balance between planning and implementation.",
			"{}"},
		{"architect", "Architect",
			"You are in architect mode. Focus on design decisions, code structure, and system patterns. Read and analyze code but do not make changes unless explicitly asked.",
			`{"prefer":["dev_read","dev_grep","dev_glob"]}`},
		{"builder", "Builder",
			"You are in builder mode. Focus on implementation. Write code, create files, and execute build commands. Be action-oriented and efficient.",
			`{"prefer":["dev_write","dev_edit","dev_bash"]}`},
	}
	for _, m := range devModes {
		if _, err := tx.Exec(
			`INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum, tool_overrides)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), "developer-001", m.slug, m.name, m.addendum, m.toolOverrides,
		); err != nil {
			return fmt.Errorf("insert developer mode %s: %w", m.slug, err)
		}
	}

	// --- Agent profile: Researcher ---
	researchPrompt := `You are Researcher, an information gathering and analysis agent. You read files, search codebases, and fetch web content to answer questions and compile research reports. You do NOT modify files or execute destructive commands. You are thorough, cite sources, and organize findings clearly.`

	if _, err := tx.Exec(
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute,
		        mcp_servers, tool_permissions)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"researcher-001", "Researcher", "researcher", researchPrompt,
		"Information gathering and analysis agent (read-only)", false,
		`["dev","general"]`, `{"allow_list":["mcp__dev__dev_read","mcp__dev__dev_grep","mcp__dev__dev_glob","mcp__general__*"],"deny_list":["mcp__dev__dev_write","mcp__dev__dev_edit","mcp__dev__dev_bash"]}`,
	); err != nil {
		return fmt.Errorf("insert researcher profile: %w", err)
	}

	// Researcher modes
	resModes := []struct {
		slug, name, addendum string
	}{
		{"default", "Default",
			"You are in your default research mode. Balance between breadth and depth."},
		{"deep-dive", "Deep Dive",
			"You are in deep-dive mode. Be thorough and exhaustive. Follow every lead, read every relevant file, and compile comprehensive reports."},
		{"quick-scan", "Quick Scan",
			"You are in quick-scan mode. Be fast and concise. Get the key facts and summarize in bullet points. Don't explore tangents."},
	}
	for _, m := range resModes {
		if _, err := tx.Exec(
			`INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum)
			 VALUES (?, ?, ?, ?, ?)`,
			uuid.New().String(), "researcher-001", m.slug, m.name, m.addendum,
		); err != nil {
			return fmt.Errorf("insert researcher mode %s: %w", m.slug, err)
		}
	}

	// --- Agent profile: Orchestrator ---
	orchPrompt := `You are Orchestrator, a project management and automation agent. You manage tasks, sprints, and workflows using Volon and Hadron. You create plans, track progress, and coordinate between agents. You do NOT write code or access files directly.`

	if _, err := tx.Exec(
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute,
		        mcp_servers, tool_permissions)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"orchestrator-001", "Orchestrator", "orchestrator", orchPrompt,
		"Project management and automation agent", false,
		`["volon","hadron"]`, `{"allow_list":["mcp__volon__*","mcp__hadron__*"]}`,
	); err != nil {
		return fmt.Errorf("insert orchestrator profile: %w", err)
	}

	// Orchestrator modes
	orchModes := []struct {
		slug, name, addendum string
	}{
		{"default", "Default",
			"You are in your default orchestration mode. Manage tasks, plan sprints, and coordinate work."},
	}
	for _, m := range orchModes {
		if _, err := tx.Exec(
			`INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum)
			 VALUES (?, ?, ?, ?, ?)`,
			uuid.New().String(), "orchestrator-001", m.slug, m.name, m.addendum,
		); err != nil {
			return fmt.Errorf("insert orchestrator mode %s: %w", m.slug, err)
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

// SeedAgentSkillBindings assigns builtin skills and prompt templates to agent profiles.
// Must be called after SeedBuiltinSkills and SeedBuiltinPromptTemplates.
func (s *Store) SeedAgentSkillBindings() error {
	// Check if bindings already exist.
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM agent_skills").Scan(&count); err != nil {
		return fmt.Errorf("check agent_skills: %w", err)
	}
	if count > 0 {
		return nil // already seeded
	}

	// Map skill slugs to IDs.
	skills, err := s.ListSkills()
	if err != nil {
		return fmt.Errorf("list skills: %w", err)
	}
	skillBySlug := make(map[string]string, len(skills))
	for _, sk := range skills {
		skillBySlug[sk.Slug] = sk.ID
	}

	// Map prompt template slugs to IDs.
	ptemplates, err := s.ListPromptTemplates()
	if err != nil {
		return fmt.Errorf("list prompt templates: %w", err)
	}
	ptBySlug := make(map[string]string, len(ptemplates))
	for _, pt := range ptemplates {
		ptBySlug[pt.Slug] = pt.ID
	}

	// Mentat: all Tiamat skills (general + encoding), can_execute=false
	mentatSkills := []string{"math-evaluate", "encoding-convert"}
	for _, slug := range mentatSkills {
		if id, ok := skillBySlug[slug]; ok {
			if err := s.AssignSkillToAgent("mentat-001", id, "{}"); err != nil {
				log.Printf("seed: failed to assign skill %s to mentat: %v", slug, err)
			}
		}
	}

	// Developer: dev tools only
	devSkills := []string{"dev-read", "dev-write", "dev-grep", "dev-bash", "dev-glob", "dev-edit"}
	for _, slug := range devSkills {
		if id, ok := skillBySlug[slug]; ok {
			if err := s.AssignSkillToAgent("developer-001", id, "{}"); err != nil {
				log.Printf("seed: failed to assign skill %s to developer: %v", slug, err)
			}
		}
	}

	// Researcher: read-only tools
	resSkills := []string{"dev-read", "dev-grep", "dev-glob", "math-evaluate", "encoding-convert"}
	for _, slug := range resSkills {
		if id, ok := skillBySlug[slug]; ok {
			if err := s.AssignSkillToAgent("researcher-001", id, "{}"); err != nil {
				log.Printf("seed: failed to assign skill %s to researcher: %v", slug, err)
			}
		}
	}

	// Orchestrator: no builtin tool skills (uses volon/hadron MCP directly)
	// Skills will be auto-discovered from MCP servers.

	// Assign prompt templates to all agents.
	allAgents := []string{"mentat-001", "developer-001", "researcher-001", "orchestrator-001"}
	commonTemplates := []string{"base-identity", "workspace-context", "mode-addendum", "tool-awareness"}
	for _, agentID := range allAgents {
		for _, slug := range commonTemplates {
			if id, ok := ptBySlug[slug]; ok {
				if err := s.AssignPromptTemplateToAgent(agentID, id); err != nil {
					log.Printf("seed: failed to assign prompt template %s to %s: %v", slug, agentID, err)
				}
			}
		}
	}

	log.Println("seed: assigned skill and prompt template bindings to agents")
	return nil
}
