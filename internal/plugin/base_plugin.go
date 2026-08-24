package plugin

import (
	"sync"
	"time"

	fplugin "github.com/hollis-labs/plugin-sdk"
	"gopkg.in/yaml.v3"
)

// ManifestLoader lazily returns a parsed plugin.yaml manifest.
type ManifestLoader func() *PluginManifest

// LoadEmbeddedManifest returns a lazy, sync.Once-backed loader for an embedded
// plugin.yaml. Invalid embedded metadata panics so builtin source errors surface
// at boot instead of producing a partially wired plugin.
func LoadEmbeddedManifest(pluginID string, manifestYAML []byte) ManifestLoader {
	var (
		once     sync.Once
		manifest *PluginManifest
	)

	return func() *PluginManifest {
		once.Do(func() {
			var m PluginManifest
			if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
				panic(pluginID + ": invalid embedded plugin.yaml: " + err.Error())
			}
			manifest = &m
		})
		return manifest
	}
}

// BasePluginConfig contains the static identity and metadata for BasePlugin.
type BasePluginConfig struct {
	ID           string
	Name         string
	Version      string
	Description  string
	Dependencies []string
	Manifest     ManifestLoader
	LogPrefix    string
}

// BasePlugin implements the common plugin-sdk identity, manifest, and lifecycle
// methods used by simple builtin plugins.
type BasePlugin struct {
	id           string
	name         string
	version      string
	description  string
	dependencies []string
	manifest     ManifestLoader
	logPrefix    string

	host   fplugin.Host
	status fplugin.PluginStatus
}

// NewBasePlugin creates a BasePlugin from static metadata. The returned value
// is intended to be embedded by builtin plugin structs.
func NewBasePlugin(cfg BasePluginConfig) BasePlugin {
	logPrefix := cfg.LogPrefix
	if logPrefix == "" {
		logPrefix = cfg.ID
	}

	var dependencies []string
	if len(cfg.Dependencies) > 0 {
		dependencies = append([]string(nil), cfg.Dependencies...)
	}

	return BasePlugin{
		id:           cfg.ID,
		name:         cfg.Name,
		version:      cfg.Version,
		description:  cfg.Description,
		dependencies: dependencies,
		manifest:     cfg.Manifest,
		logPrefix:    logPrefix,
	}
}

// ID returns the plugin identifier.
func (p *BasePlugin) ID() string { return p.id }

// Name returns the human-readable plugin name.
func (p *BasePlugin) Name() string { return p.name }

// Version returns the plugin version.
func (p *BasePlugin) Version() string { return p.version }

// Description returns the plugin description.
func (p *BasePlugin) Description() string { return p.description }

// Dependencies returns the plugin dependency IDs.
func (p *BasePlugin) Dependencies() []string {
	if len(p.dependencies) == 0 {
		return nil
	}
	return append([]string(nil), p.dependencies...)
}

// Manifest exposes the embedded plugin.yaml when configured.
func (p *BasePlugin) Manifest() *PluginManifest {
	if p.manifest == nil {
		return nil
	}
	return p.manifest()
}

// Load records loaded status and emits the standard builtin lifecycle log.
func (p *BasePlugin) Load(host fplugin.Host) error {
	p.host = host
	p.status = fplugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	host.Logger().Info(p.logPrefix + ": loaded")
	return nil
}

// Unload records unloaded status and emits the standard builtin lifecycle log.
func (p *BasePlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info(p.logPrefix + ": unloaded")
	}
	return nil
}

// Status returns the current plugin lifecycle status.
func (p *BasePlugin) Status() fplugin.PluginStatus {
	return p.status
}
