package observabilitywidgets

import (
	_ "embed"
	"sync"
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/plugin-sdk"
	"gopkg.in/yaml.v3"
)

//go:embed plugin.yaml
var manifestYAML []byte

var (
	parsedManifestOnce sync.Once
	parsedManifest     *hostplugin.PluginManifest
)

func loadManifest() *hostplugin.PluginManifest {
	parsedManifestOnce.Do(func() {
		var m hostplugin.PluginManifest
		if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
			panic("observability-widgets: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("observability-widgets", func() plugin.Plugin { return New() })
}

// ObservabilityWidgetsPlugin provides LLM execution metrics widgets.
type ObservabilityWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *ObservabilityWidgetsPlugin { return &ObservabilityWidgetsPlugin{} }

func (p *ObservabilityWidgetsPlugin) ID() string             { return "observability-widgets" }
func (p *ObservabilityWidgetsPlugin) Name() string           { return "Observability" }
func (p *ObservabilityWidgetsPlugin) Version() string        { return "1.0.0" }
func (p *ObservabilityWidgetsPlugin) Description() string    { return "LLM execution metrics, duration, cost, and error tracking widget" }
func (p *ObservabilityWidgetsPlugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so widget UIComponents register
// through the host's yaml-authoritative loader path (H.3 / B.4).
func (p *ObservabilityWidgetsPlugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *ObservabilityWidgetsPlugin) Load(host plugin.Host) error {
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("observability-widgets plugin loaded (yaml-authoritative)")
	return nil
}

func (p *ObservabilityWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *ObservabilityWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
