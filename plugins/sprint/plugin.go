package sprint

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/conduit/internal/chat"
	"github.com/hollis-labs/conduit/internal/mcp"
	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("sprint", func() plugin.Plugin { return New() })
}

// SprintPlugin provides sprint planning, backlog management, and Volon
// integration as a self-contained Conduit plugin.
type SprintPlugin struct {
	host       plugin.Host
	mcpManager *mcp.Manager
	status     plugin.PluginStatus
}

func New() *SprintPlugin {
	return &SprintPlugin{}
}

func (p *SprintPlugin) ID() string          { return "sprint" }
func (p *SprintPlugin) Name() string        { return "Sprint Planning" }
func (p *SprintPlugin) Version() string     { return "0.1.0" }
func (p *SprintPlugin) Description() string { return "Sprint planning, backlog grooming, and Volon/Engine integration" }
func (p *SprintPlugin) Dependencies() []string { return nil }

func (p *SprintPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Get MCP manager for sprint tool registration.
	if svc, err := host.GetService("mcp"); err == nil {
		if mgr, ok := svc.(*mcp.Manager); ok {
			p.mcpManager = mgr
		}
	}

	// Register the envelope type so it passes backend validation.
	chat.RegisterEnvelopeType("sprint-planning-review")

	// Register sprint-planning-review envelope component.
	envComponent := plugin.UIComponent{
		ID:          "sprint-planning-review",
		Type:        plugin.UIComponentTypeEnvelope,
		Name:        "Sprint Planning Review",
		Description: "Interactive sprint planning review card with task assignment",
	}
	if err := host.RegisterUIComponent(envComponent); err != nil {
		return fmt.Errorf("failed to register sprint-planning-review envelope: %w", err)
	}

	// Register sprint MCP tools (open_sprint_planning + show_sprint_planning_review).
	if p.mcpManager != nil {
		tools := NewSprintToolsTransport(p.mcpManager)
		p.mcpManager.AddServer("sprint-tools", tools)
		logger.Info("sprint planning tools registered as MCP server")
	}

	// Register event hook for workflow events related to sprints.
	hook := &sprintEventHook{logger: logger}
	if err := host.RegisterEventHook([]string{"workflow.completed"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("sprint plugin loaded", "version", p.Version())
	return nil
}

func (p *SprintPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("sprint plugin unloaded")
	}
	return nil
}

func (p *SprintPlugin) Status() plugin.PluginStatus {
	return p.status
}

type sprintEventHook struct {
	logger plugin.Logger
}

func (h *sprintEventHook) Handle(ctx context.Context, event plugin.Event) error {
	h.logger.Debug("sprint: received event", "type", event.Type, "session", event.SessionID)
	return nil
}

func (h *sprintEventHook) EventTypes() []string {
	return []string{"workflow.completed"}
}
