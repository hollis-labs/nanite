package sessionstats

import (
	"database/sql"
	"fmt"
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/plugin"
)

func init() {
	hostplugin.RegisterPlugin("session-stats", func() plugin.Plugin { return New() })
}

// SessionStatsPlugin tracks per-session statistics.
type SessionStatsPlugin struct {
	host   plugin.Host
	store  *SessionStatsStore
	status plugin.PluginStatus
}

// New creates a new SessionStatsPlugin instance.
func New() *SessionStatsPlugin {
	return &SessionStatsPlugin{}
}

func (p *SessionStatsPlugin) ID() string            { return "session-stats" }
func (p *SessionStatsPlugin) Name() string          { return "Session Stats" }
func (p *SessionStatsPlugin) Version() string       { return "1.0.0" }
func (p *SessionStatsPlugin) Description() string   { return "Session statistics tracking and display plugin" }
func (p *SessionStatsPlugin) Dependencies() []string { return nil }

func (p *SessionStatsPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Get the SQLite DB from the store service.
	var db *sql.DB
	if svc, err := host.GetService("store"); err == nil {
		type hasDB interface{ GetSQLDB() *sql.DB }
		if s, ok := svc.(hasDB); ok {
			db = s.GetSQLDB()
		} else if st, ok := svc.(*nanitestore.Store); ok {
			db = st.DB
		}
	}

	if db == nil {
		return fmt.Errorf("session-stats plugin requires the store service with a SQL DB")
	}

	p.store = NewSessionStatsStore(db)
	if err := p.store.InitSchema(); err != nil {
		return fmt.Errorf("init session_stats schema: %w", err)
	}

	// Register API routes via UI components with handlers.
	statsGetComponent := plugin.UIComponent{
		ID:          "session-stats-get",
		Type:        plugin.UIComponentTypeWidget,
		Name:        "Session Stats",
		Description: "Get session statistics",
		Handler:     GetSessionStatsHandler(p.store),
	}
	if err := host.RegisterUIComponent(statsGetComponent); err != nil {
		return fmt.Errorf("register session-stats-get: %w", err)
	}

	statsUpdateComponent := plugin.UIComponent{
		ID:          "session-stats-update",
		Type:        plugin.UIComponentTypeWidget,
		Name:        "Update Session Stats",
		Description: "Update session statistics",
		Handler:     UpdateSessionStatsHandler(p.store),
	}
	if err := host.RegisterUIComponent(statsUpdateComponent); err != nil {
		return fmt.Errorf("register session-stats-update: %w", err)
	}

	exportComponent := plugin.UIComponent{
		ID:          "session-stats-export",
		Type:        plugin.UIComponentTypeWidget,
		Name:        "Export Session Stats",
		Description: "Export session statistics as CSV",
		Handler:     ExportSessionStatsHandler(p.store),
	}
	if err := host.RegisterUIComponent(exportComponent); err != nil {
		return fmt.Errorf("register session-stats-export: %w", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("session-stats plugin loaded", "version", p.Version())
	return nil
}

func (p *SessionStatsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("session-stats plugin unloaded")
	}
	return nil
}

func (p *SessionStatsPlugin) Status() plugin.PluginStatus {
	return p.status
}
