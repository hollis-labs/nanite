package contextwidgets

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
			panic("context-widgets: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("context-widgets", func() plugin.Plugin { return New() })
}

// ContextWidgetsPlugin provides session context and token usage widgets.
type ContextWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *ContextWidgetsPlugin { return &ContextWidgetsPlugin{} }

func (p *ContextWidgetsPlugin) ID() string             { return "context-widgets" }
func (p *ContextWidgetsPlugin) Name() string           { return "Context & Usage" }
func (p *ContextWidgetsPlugin) Version() string        { return "1.0.0" }
func (p *ContextWidgetsPlugin) Description() string    { return "Session info, context budget, and token usage widgets" }
func (p *ContextWidgetsPlugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so widget UIComponents register
// through the host's yaml-authoritative loader path (H.3 / B.4).
func (p *ContextWidgetsPlugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *ContextWidgetsPlugin) Load(host plugin.Host) error {
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("context-widgets plugin loaded (yaml-authoritative)")
	return nil
}

func (p *ContextWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *ContextWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
