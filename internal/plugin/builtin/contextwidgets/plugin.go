package contextwidgets

import (
	"time"

	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("context-widgets", func() plugin.Plugin { return New() })
}

// ContextWidgetsPlugin provides session context and token usage widgets.
type ContextWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *ContextWidgetsPlugin { return &ContextWidgetsPlugin{} }

func (p *ContextWidgetsPlugin) ID() string            { return "context-widgets" }
func (p *ContextWidgetsPlugin) Name() string          { return "Context & Usage" }
func (p *ContextWidgetsPlugin) Version() string       { return "1.0.0" }
func (p *ContextWidgetsPlugin) Description() string   { return "Session info, context budget, and token usage widgets" }
func (p *ContextWidgetsPlugin) Dependencies() []string { return nil }

func (p *ContextWidgetsPlugin) Load(host plugin.Host) error {
	widgets := []plugin.UIComponent{
		{
			ID:          "session-info",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Session Info",
			Description: "Active session metadata (title, short code, message count, timestamps)",
		},
		{
			ID:          "context-budget",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Context Budget",
			Description: "Context window usage bars, token breakdown, and context inspector",
		},
		{
			ID:          "token-usage",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Token Usage",
			Description: "Session and cumulative token costs with per-direction breakdown",
		},
	}

	for _, w := range widgets {
		if err := host.RegisterUIComponent(w); err != nil {
			return err
		}
	}

	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("context-widgets plugin loaded", "widgets", len(widgets))
	return nil
}

func (p *ContextWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *ContextWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
