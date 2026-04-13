package agentwidgets

import (
	_ "embed"
	"sync"
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/go-plugin"
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
			panic("agent-widgets: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("agent-widgets", func() plugin.Plugin { return New() })
}

// AgentWidgetsPlugin provides agent status and MCP tools widgets.
type AgentWidgetsPlugin struct {
	status plugin.PluginStatus
}

func New() *AgentWidgetsPlugin { return &AgentWidgetsPlugin{} }

func (p *AgentWidgetsPlugin) ID() string             { return "agent-widgets" }
func (p *AgentWidgetsPlugin) Name() string           { return "Agent & Tools" }
func (p *AgentWidgetsPlugin) Version() string        { return "1.0.0" }
func (p *AgentWidgetsPlugin) Description() string    { return "Agent mode switching and MCP tool discovery widgets" }
func (p *AgentWidgetsPlugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so widget UIComponents register
// through the host's yaml-authoritative loader path (H.3 / B.4).
func (p *AgentWidgetsPlugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *AgentWidgetsPlugin) Load(host plugin.Host) error {
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("agent-widgets plugin loaded (yaml-authoritative)")
	return nil
}

func (p *AgentWidgetsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *AgentWidgetsPlugin) Status() plugin.PluginStatus { return p.status }
