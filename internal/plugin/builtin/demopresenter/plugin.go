package demopresenter

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	conduitstore "github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("demo-presenter", func() plugin.Plugin { return New() })
}

// DemoPresenterPlugin provides the demo presenter agent for interactive
// Fragments Engine presentations. It seeds a read-only agent profile that
// composes envelope data directly instead of creating real database records.
type DemoPresenterPlugin struct {
	host   plugin.Host
	status plugin.PluginStatus
}

// New creates a new DemoPresenterPlugin instance.
func New() *DemoPresenterPlugin {
	return &DemoPresenterPlugin{}
}

func (p *DemoPresenterPlugin) ID() string          { return "demo-presenter" }
func (p *DemoPresenterPlugin) Name() string        { return "Demo Presenter" }
func (p *DemoPresenterPlugin) Version() string     { return "0.1.0" }
func (p *DemoPresenterPlugin) Description() string { return "Demo presenter agent for interactive Fragments Engine presentations" }
func (p *DemoPresenterPlugin) Dependencies() []string { return nil }

func (p *DemoPresenterPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Seed the demo-presenter agent profile if it doesn't exist.
	if err := seedAgent(host); err != nil {
		logger.Warn("failed to seed Demo Presenter agent", "error", fmt.Sprintf("%v", err))
		// Non-fatal — the plugin still loads, just no dedicated agent profile.
	}

	// NOTE: The 6 demo-specific tools (conduit_navigate_engine, conduit_refresh_engine,
	// conduit_show_report, conduit_show_document, conduit_show_task_disposition,
	// conduit_show_sprint_planning_review, conduit_show_giphy, conduit_run_report)
	// are registered globally in internal/mcp/self_tools.go. They are available to
	// all agents and referenced by name in the agent's tool_permissions allow_list.
	// TODO: Consider moving demo-specific tools into this plugin via host.GetService("mcp")
	// registration during Load(), similar to how support-ticket registers KB search.

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("demo-presenter plugin loaded", "version", p.Version())
	return nil
}

func (p *DemoPresenterPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("demo-presenter plugin unloaded")
	}
	return nil
}

// Uninstall removes artifacts created by the plugin (agent profile).
func (p *DemoPresenterPlugin) Uninstall(host plugin.Host) error {
	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("get store service: %w", err)
	}
	db, ok := svc.(*conduitstore.Store)
	if !ok {
		return fmt.Errorf("store service is %T, expected *store.Store", svc)
	}
	if err := db.DeleteAgent("demo-presenter"); err != nil {
		return fmt.Errorf("remove Demo Presenter agent: %w", err)
	}
	host.Logger().Info("uninstalled demo-presenter plugin — removed Demo Presenter agent profile")
	return nil
}

func (p *DemoPresenterPlugin) Status() plugin.PluginStatus {
	return p.status
}

// seedAgent ensures the Demo Presenter agent profile exists in the database.
// It is idempotent — if the agent already exists, it does nothing.
func seedAgent(host plugin.Host) error {
	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("get store service: %w", err)
	}

	db, ok := svc.(*conduitstore.Store)
	if !ok {
		return fmt.Errorf("store service is %T, expected *store.Store", svc)
	}

	// Check if the agent already exists.
	_, err = db.GetAgentBySlug("demo-presenter")
	if err == nil {
		// Agent already exists — nothing to do.
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing agent: %w", err)
	}

	agent := &conduitstore.AgentProfile{
		ID:           "demo-presenter-001",
		Name:         "Demo Presenter",
		Slug:         "demo-presenter",
		Description:  "AI-powered demo agent for presentations — read-only Engine access, composes envelope data directly",
		DefaultModel: "claude-sonnet-4-20250514",
		DefaultMode:  "default",
		CanExecute:   true,
		MCPServers:   `["engine","cortex"]`,
		ToolPermissions: `{"allow_list":["conduit_*","mcp__engine__engine_tasks_list","mcp__engine__engine_task_get","mcp__engine__engine_task_search","mcp__engine__engine_sprints_list","mcp__engine__engine_sprint_get","mcp__engine__engine_epics_list","mcp__engine__engine_epic_get","mcp__engine__engine_projects_list","mcp__engine__engine_portfolio_summary","mcp__engine__engine_portfolio_health","mcp__cortex__*"]}`,
		Modes:        `[]`,
		Settings:     `{}`,
		SystemPrompt: demoPresenterSystemPrompt,
	}

	if err := db.CreateAgent(agent); err != nil {
		return fmt.Errorf("create Demo Presenter agent: %w", err)
	}

	host.Logger().Info("seeded Demo Presenter agent profile", "slug", "demo-presenter")
	return nil
}

const demoPresenterSystemPrompt = `You are the Fragments Engine Demo Presenter — a sophisticated AI assistant
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
