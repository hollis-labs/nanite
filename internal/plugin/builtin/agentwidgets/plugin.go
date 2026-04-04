package agentwidgets

import (
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/plugin"
)

func init() {
	hostplugin.RegisterPlugin("agent-widgets", func() plugin.Plugin { return New() })
}

// AgentWidgetsPlugin provides agent status and MCP tools widgets.
type AgentWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *AgentWidgetsPlugin { return &AgentWidgetsPlugin{} }

func (p *AgentWidgetsPlugin) ID() string            { return "agent-widgets" }
func (p *AgentWidgetsPlugin) Name() string          { return "Agent & Tools" }
func (p *AgentWidgetsPlugin) Version() string       { return "1.0.0" }
func (p *AgentWidgetsPlugin) Description() string   { return "Agent mode switching and MCP tool discovery widgets" }
func (p *AgentWidgetsPlugin) Dependencies() []string { return nil }

func (p *AgentWidgetsPlugin) Load(host plugin.Host) error {
	widgets := []plugin.UIComponent{
		{
			ID:          "agent-status",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Agent Status",
			Description: "Current agent mode and model selection with mode switching",
		},
		{
			ID:          "tools",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Tools",
			Description: "MCP server health and tool discovery status",
		},
	}

	for _, w := range widgets {
		if err := host.RegisterUIComponent(w); err != nil {
			return err
		}
	}

	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("agent-widgets plugin loaded", "widgets", len(widgets))
	return nil
}

func (p *AgentWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *AgentWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
