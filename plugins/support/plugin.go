package support

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/hollis-labs/conduit/internal/mcp"
	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	conduitstore "github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("support", func() plugin.Plugin { return New() })
}

// SupportPlugin implements the IT self-service support plugin for Conduit.
// It provides an in-memory ticket store with CRUD endpoints and a ticket
// download page for BMC Helix POC demos.
type SupportPlugin struct {
	host   plugin.Host
	store  *TicketStore
	status plugin.PluginStatus
}

// New creates a new SupportPlugin instance.
func New() *SupportPlugin {
	return &SupportPlugin{
		store: NewTicketStore(),
	}
}

func (p *SupportPlugin) ID() string          { return "support" }
func (p *SupportPlugin) Name() string        { return "IT Support" }
func (p *SupportPlugin) Version() string     { return "0.1.0" }
func (p *SupportPlugin) Description() string { return "IT employee self-service support — KB search + ticket creation" }
func (p *SupportPlugin) Dependencies() []string { return nil }

func (p *SupportPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Wire ticket store to Conduit's SQLite DB for persistence across restarts.
	if svc, err := host.GetService("store"); err == nil {
		type hasDB interface{ GetSQLDB() *sql.DB }
		if s, ok := svc.(hasDB); ok {
			p.store.SetDB(s.GetSQLDB())
			logger.Info("ticket store backed by SQLite")
		} else {
			// Try direct field access via the store package
			if st, ok := svc.(*conduitstore.Store); ok {
				p.store.SetDB(st.DB)
				logger.Info("ticket store backed by SQLite")
			}
		}
	}

	// Register CRUD handler for tickets — auto-wires /api/plugins/tickets/*
	handler := NewTicketHandler(p.store)
	if err := host.RegisterCRUDHandler("tickets", handler); err != nil {
		return fmt.Errorf("failed to register tickets CRUD handler: %w", err)
	}

	// Register ticket download UI component
	downloadHandler := NewDownloadHandler(p.store)
	component := plugin.UIComponent{
		ID:          "support-ticket-download",
		Type:        plugin.UIComponentTypeEnvelope,
		Name:        "Ticket Download",
		Description: "Download a formatted HTML ticket for print/PDF",
		Handler:     downloadHandler,
	}
	if err := host.RegisterUIComponent(component); err != nil {
		return fmt.Errorf("failed to register ticket download component: %w", err)
	}

	// Register event hook for message.sent
	hook := &supportEventHook{logger: logger}
	if err := host.RegisterEventHook([]string{"message.sent"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	// Seed the IT Support agent profile if it doesn't exist
	if err := seedAgent(host); err != nil {
		logger.Warn("failed to seed IT Support agent", "error", fmt.Sprintf("%v", err))
		// Non-fatal — the plugin still works, just no dedicated agent profile
	}

	// Resolve KB database URL from config (env var → config file → default).
	dbURL, cfgErr := host.GetConfig("database_url")
	if cfgErr != nil {
		// Fallback for backward compatibility when config is not loaded.
		dbURL = "host=localhost port=5432 dbname=kb_demo sslmode=disable"
		logger.Warn("config unavailable, using default database_url", "error", fmt.Sprintf("%v", cfgErr))
	}

	// Initialize KB search transport and register with MCP manager.
	kbTransport, err := NewKBTransport(dbURL)
	if err != nil {
		logger.Warn("KB search unavailable — database not connected", "error", fmt.Sprintf("%v", err))
	} else {
		mcpSvc, mcpErr := host.GetService("mcp")
		if mcpErr == nil {
			if mgr, ok := mcpSvc.(*mcp.Manager); ok {
				mgr.AddServer("support-kb", kbTransport)
				logger.Info("KB search registered as MCP server", "server", "support-kb")
			}
		}
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("support plugin loaded", "version", p.Version())
	return nil
}

func (p *SupportPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("support plugin unloaded")
	}
	return nil
}

func (p *SupportPlugin) Status() plugin.PluginStatus {
	return p.status
}

// supportEventHook listens for message.sent events (placeholder for future KB search triggers).
type supportEventHook struct {
	logger plugin.Logger
}

func (h *supportEventHook) Handle(ctx context.Context, event plugin.Event) error {
	h.logger.Debug("support: received event", "type", event.Type, "session", event.SessionID)
	return nil
}

func (h *supportEventHook) EventTypes() []string {
	return []string{"message.sent"}
}
