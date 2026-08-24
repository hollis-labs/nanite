package debugwidgets

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
			panic("debug-widgets: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("debug-widgets", func() plugin.Plugin { return New() })
}

// DebugWidgetsPlugin provides developer-only debug widgets: slot inspector
// and turn snapshots. Activates only when developer_mode is true.
//
// The former "broker decisions" widget (SQL-backed via the now-dropped
// broker_decisions table) was removed by
// TASKS/phase-0/23-export-and-drop-decision-tables.md — its replacement is
// the inspector-backed Broker tab in
// ui/src/components/settings/inspector/InspectorPanel.tsx.
type DebugWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *DebugWidgetsPlugin { return &DebugWidgetsPlugin{} }

func (p *DebugWidgetsPlugin) ID() string      { return "debug-widgets" }
func (p *DebugWidgetsPlugin) Name() string    { return "Debug Widgets" }
func (p *DebugWidgetsPlugin) Version() string { return "1.0.0" }
func (p *DebugWidgetsPlugin) Description() string {
	return "Slot inspector and turn snapshot widgets (developer_mode only)"
}
func (p *DebugWidgetsPlugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so widget UIComponents register
// through the host's yaml-authoritative loader path (H.3 / B.4).
func (p *DebugWidgetsPlugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *DebugWidgetsPlugin) Load(host plugin.Host) error {
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("debug-widgets plugin loaded (yaml-authoritative)")
	return nil
}

func (p *DebugWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *DebugWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
