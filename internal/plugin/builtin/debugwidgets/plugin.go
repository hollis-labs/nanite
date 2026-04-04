package debugwidgets

import (
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/plugin"
)

func init() {
	hostplugin.RegisterPlugin("debug-widgets", func() plugin.Plugin { return New() })
}

// DebugWidgetsPlugin provides developer-only debug widgets:
// broker decisions, slot inspector, and turn snapshots.
// Activates only when developer_mode is true.
type DebugWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *DebugWidgetsPlugin { return &DebugWidgetsPlugin{} }

func (p *DebugWidgetsPlugin) ID() string            { return "debug-widgets" }
func (p *DebugWidgetsPlugin) Name() string          { return "Debug Widgets" }
func (p *DebugWidgetsPlugin) Version() string       { return "1.0.0" }
func (p *DebugWidgetsPlugin) Description() string   { return "Broker decisions, slot inspector, and turn snapshot widgets (developer_mode only)" }
func (p *DebugWidgetsPlugin) Dependencies() []string { return nil }

func (p *DebugWidgetsPlugin) Load(host plugin.Host) error {
	widgets := []plugin.UIComponent{
		{
			ID:          "broker-decisions",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Broker Decisions",
			Description: "Tool broker decision log with layer, intent, and selected tools",
		},
		{
			ID:          "slot-inspector",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Context Slots",
			Description: "Context window slot allocation and token budget breakdown",
		},
		{
			ID:          "turn-snapshots",
			Type:        plugin.UIComponentTypeWidget,
			Name:        "Turn Snapshots",
			Description: "Per-turn execution snapshots with tool call timings",
		},
	}

	for _, w := range widgets {
		if err := host.RegisterUIComponent(w); err != nil {
			return err
		}
	}

	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("debug-widgets plugin loaded", "widgets", len(widgets))
	return nil
}

func (p *DebugWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *DebugWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
