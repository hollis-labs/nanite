package observabilitywidgets

import (
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/plugin"
)

func init() {
	hostplugin.RegisterPlugin("observability-widgets", func() plugin.Plugin { return New() })
}

// ObservabilityWidgetsPlugin provides LLM execution metrics widgets.
type ObservabilityWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *ObservabilityWidgetsPlugin { return &ObservabilityWidgetsPlugin{} }

func (p *ObservabilityWidgetsPlugin) ID() string            { return "observability-widgets" }
func (p *ObservabilityWidgetsPlugin) Name() string          { return "Observability" }
func (p *ObservabilityWidgetsPlugin) Version() string       { return "1.0.0" }
func (p *ObservabilityWidgetsPlugin) Description() string   { return "LLM execution metrics, duration, cost, and error tracking widget" }
func (p *ObservabilityWidgetsPlugin) Dependencies() []string { return nil }

func (p *ObservabilityWidgetsPlugin) Load(host plugin.Host) error {
	w := plugin.UIComponent{
		ID:          "observability",
		Type:        plugin.UIComponentTypeWidget,
		Name:        "Observability",
		Description: "Recent LLM execution metrics (duration, cost, errors)",
	}
	if err := host.RegisterUIComponent(w); err != nil {
		return err
	}

	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("observability-widgets plugin loaded")
	return nil
}

func (p *ObservabilityWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *ObservabilityWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
