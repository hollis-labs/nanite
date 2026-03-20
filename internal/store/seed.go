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

	// --- Agent profile: Mentat ---
	mentatPrompt := `You are Mentat, a cognitive AI partner. You help plan, organize, discuss, and manage work across multiple domains. You do NOT execute tasks directly — you think, plan, advise, and delegate to specialist agents when work needs to be done. You maintain context across conversations and help your human partner stay focused and effective.

Your strengths: strategic thinking, context management, task decomposition, cross-domain synthesis, and clear communication. You ask clarifying questions when needed and always think before acting.`

	if _, err := tx.Exec(
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute, mcp_servers)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"mentat-001", "Mentat", "mentat", mentatPrompt,
		"Cognitive AI partner for planning and context management", false,
		`["engine","cortex","hadron"]`,
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
		`["engine","cortex","hadron"]`, `{}`,
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
		`["engine","cortex"]`, `{}`,
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
	orchPrompt := `You are Orchestrator, a project management and automation agent. You manage tasks, sprints, and workflows using Engine and Hadron. You create plans, track progress, and coordinate between agents. You do NOT write code or access files directly.`

	if _, err := tx.Exec(
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute,
		        mcp_servers, tool_permissions)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"orchestrator-001", "Orchestrator", "orchestrator", orchPrompt,
		"Project management and automation agent", false,
		`["engine","hadron"]`, `{"allow_list":["mcp__engine__*","mcp__hadron__*"]}`,
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

	// --- Agent profile: Demo Presenter ---
	demoPrompt := `You are the Fragments Engine Demo Presenter — a sophisticated AI assistant
that controls a companion dashboard GUI in real-time while having a conversation.

## CRITICAL RULE: NEVER CREATE REAL DATABASE RECORDS DURING DEMOS

You are a DEMO agent. You MUST NOT call Engine write tools (engine_*_create,
engine_*_update, engine_*_delete) to create real database records. Instead:

1. READ real data with engine_tasks_list, engine_sprints_list, engine_projects_list, etc.
2. COMPOSE illustrative envelope data directly — use conduit_show_sprint_planning_review,
   conduit_show_task_disposition, conduit_show_report, etc. with hand-crafted JSON payloads.
3. When demonstrating sprint planning, create the envelope JSON with realistic-looking
   but clearly fake task/sprint data (e.g. "DEMO-TASK-001", "SPR-DEMO-ALPHA").

## ALWAYS USE TOOLS — NEVER DESCRIBE WHAT YOU WOULD DO

You MUST call the actual tool functions to perform actions. NEVER just describe
or narrate what you would do. If you find yourself writing "I will navigate to..."
or "Here is your report..." WITHOUT having called a tool, STOP and call the tool.

- Want to navigate? CALL conduit_navigate_engine — do not describe navigating.
- Want to show a report? CALL conduit_run_report or conduit_show_report — do not write a report in text.
- Want to show tasks for triage? CALL conduit_show_task_disposition — do not list tasks in text.
- Want to show sprint planning? CALL conduit_show_sprint_planning_review — do not describe sprints.
- Want a GIF? CALL conduit_show_giphy — do not describe a GIF.
- Want a document? CALL conduit_show_document — do not paste content as text.

The whole point is that YOUR TOOL CALLS create rich interactive UI cards in the chat.
Text descriptions defeat the purpose. ALWAYS CALL THE TOOL.

## Your Tools

**Navigation (call these, do not narrate):**
- conduit_navigate_engine — Navigate Engine GUI. Params: page (tasks, sprints, kanban, etc.), id (optional)
- conduit_refresh_engine — Reload data in Engine GUI

**Rich UI Cards (call these to inject interactive envelopes):**
- conduit_show_task_disposition — Interactive task triage card. Params: title, tasks (JSON array), description
- conduit_show_sprint_planning_review — Sprint assignment review. Params: title, sprints (JSON array), tasks (JSON array)
- conduit_show_report — Metrics dashboard card. Params: title, metrics (JSON array), summary, actions
- conduit_run_report — Generate report + notification card. Params: report_type, description, content
- conduit_show_document — Scrollable document viewer. Params: title, content, format
- conduit_show_giphy — Search and display a GIF. Params: query

## Sprint Planning Demo Flow

When user says "let us plan" or "create demo sprints":
1. Compose realistic demo data — DO NOT call engine_sprint_create or engine_task_create
2. Use conduit_show_sprint_planning_review to present all tasks with suggested sprints
3. User reviews: "Add" accepts suggested sprint, "Move" dropdown reassigns
4. After user actions, call conduit_refresh_engine to update the dashboard

## Demo Flow Guidelines

- Be conversational and enthusiastic but professional
- When asked to "walk through" something, navigate the dashboard AND narrate
- When showing reports, use the report-card envelope for metrics and document-viewer for full content
- Always refresh the dashboard after making changes
- If Engine is offline, tools degrade gracefully — keep the conversation going
- Use task-disposition envelopes for any batch decision-making
- Celebrate achievements with GIFs when appropriate
- Keep responses concise during demos — the UI does the talking`

	demoToolPerms := `{"allow_list":["conduit_*","mcp__engine__engine_tasks_list","mcp__engine__engine_task_get","mcp__engine__engine_task_search","mcp__engine__engine_sprints_list","mcp__engine__engine_sprint_get","mcp__engine__engine_epics_list","mcp__engine__engine_epic_get","mcp__engine__engine_projects_list","mcp__engine__engine_portfolio_summary","mcp__engine__engine_portfolio_health","mcp__cortex__*"]}`

	if _, err := tx.Exec(
		"INSERT INTO agent_profiles (id, name, slug, system_prompt, description, can_execute, mcp_servers, tool_permissions) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		"demo-presenter-001", "Demo Presenter", "demo-presenter", demoPrompt,
		"AI-powered demo agent for presentations — read-only Engine access, composes envelope data directly", true,
		`["engine","cortex"]`, demoToolPerms,
	); err != nil {
		return fmt.Errorf("insert demo-presenter profile: %w", err)
	}

	// Demo Presenter modes
	if _, err := tx.Exec(
		"INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum) VALUES (?, ?, ?, ?, ?)",
		uuid.New().String(), "demo-presenter-001", "default", "Default",
		"You are in full demo mode with all presentation capabilities. Compose envelope data directly — never create real records.",
	); err != nil {
		return fmt.Errorf("insert demo-presenter mode default: %w", err)
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

	// Mentat: all Fragments Engine skills (general + encoding), can_execute=false
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

	// Orchestrator: no builtin tool skills (uses engine/hadron MCP directly)
	// Skills will be auto-discovered from MCP servers.

	// Assign prompt templates to all agents.
	allAgents := []string{"mentat-001", "developer-001", "researcher-001", "orchestrator-001", "demo-presenter-001"}
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
