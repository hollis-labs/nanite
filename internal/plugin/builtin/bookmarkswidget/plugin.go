package bookmarkswidget

import (
	"time"

	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("bookmarks-widget", func() plugin.Plugin { return New() })
}

// BookmarksWidgetPlugin provides the bookmarks quick-access widget.
type BookmarksWidgetPlugin struct {
	status plugin.PluginStatus
}

func New() *BookmarksWidgetPlugin { return &BookmarksWidgetPlugin{} }

func (p *BookmarksWidgetPlugin) ID() string            { return "bookmarks-widget" }
func (p *BookmarksWidgetPlugin) Name() string          { return "Bookmarks" }
func (p *BookmarksWidgetPlugin) Version() string       { return "1.0.0" }
func (p *BookmarksWidgetPlugin) Description() string   { return "Quick access to bookmarked messages with scroll-to navigation" }
func (p *BookmarksWidgetPlugin) Dependencies() []string { return nil }

func (p *BookmarksWidgetPlugin) Load(host plugin.Host) error {
	w := plugin.UIComponent{
		ID:          "bookmarks",
		Type:        plugin.UIComponentTypeWidget,
		Name:        "Bookmarks",
		Description: "Quick access to bookmarked messages with scroll-to navigation",
	}
	if err := host.RegisterUIComponent(w); err != nil {
		return err
	}

	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("bookmarks-widget plugin loaded")
	return nil
}

func (p *BookmarksWidgetPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *BookmarksWidgetPlugin) Status() plugin.PluginStatus { return p.status }
