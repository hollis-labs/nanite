package bookmarks

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

// loadManifest parses the embedded plugin.yaml exactly once.
//
// The manifest is a compiled-in build artifact; a parse failure means the
// builtin was shipped with invalid source metadata and its yaml-authoritative
// registrations would silently be skipped, leaving the plugin partially
// registered. Fail fast so the bug is caught at boot rather than producing a
// broken, half-wired plugin at runtime.
func loadManifest() *hostplugin.PluginManifest {
	parsedManifestOnce.Do(func() {
		var m hostplugin.PluginManifest
		if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
			panic("bookmarks-widget: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("bookmarks-widget", func() plugin.Plugin { return New() })
}

// BookmarksWidgetPlugin provides the bookmarks quick-access widget.
type BookmarksWidgetPlugin struct {
	status plugin.PluginStatus
}

func New() *BookmarksWidgetPlugin { return &BookmarksWidgetPlugin{} }

func (p *BookmarksWidgetPlugin) ID() string             { return "bookmarks-widget" }
func (p *BookmarksWidgetPlugin) Name() string           { return "Bookmarks" }
func (p *BookmarksWidgetPlugin) Version() string        { return "1.0.0" }
func (p *BookmarksWidgetPlugin) Description() string    { return "Quick access to bookmarked messages with scroll-to navigation" }
func (p *BookmarksWidgetPlugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so the host loader can apply
// yaml-authoritative registrations on our behalf (B.4). Implementing this
// ManifestProvider interface means Load() no longer needs to call
// host.RegisterUIComponent directly — the loader does it from the manifest.
func (p *BookmarksWidgetPlugin) Manifest() *hostplugin.PluginManifest {
	return loadManifest()
}

func (p *BookmarksWidgetPlugin) Load(host plugin.Host) error {
	// UIComponent registration now flows through the yaml-authoritative
	// loader path via Manifest() (B.4). Load() only performs runtime state
	// initialization for this builtin.
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("bookmarks-widget plugin loaded (yaml-authoritative)")
	return nil
}

func (p *BookmarksWidgetPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *BookmarksWidgetPlugin) Status() plugin.PluginStatus { return p.status }
