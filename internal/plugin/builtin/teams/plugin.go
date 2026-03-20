package teams

import (
	"fmt"
	"time"

	"github.com/hollis-labs/conduit/internal/mcp"
	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/fragments-engine/connectors/webhook"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("teams", func() plugin.Plugin { return New() })
}

// TeamsPlugin integrates Microsoft Teams via Incoming Webhooks.
// It registers the teams_send_message MCP tool that agents can call
// to push Adaptive Cards to Teams channels.
type TeamsPlugin struct {
	host       plugin.Host
	client     *webhook.Client
	webhookURL string
	status     plugin.PluginStatus
}

// New creates a new TeamsPlugin instance.
func New() *TeamsPlugin {
	return &TeamsPlugin{}
}

func (p *TeamsPlugin) ID() string          { return "teams" }
func (p *TeamsPlugin) Name() string        { return "Microsoft Teams" }
func (p *TeamsPlugin) Version() string     { return "0.1.0" }
func (p *TeamsPlugin) Description() string { return "Microsoft Teams integration — push messages and cards to Teams channels" }
func (p *TeamsPlugin) Dependencies() []string { return nil }

func (p *TeamsPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Resolve webhook URL from config (env var → config file).
	webhookURL, err := host.GetConfig("teams_webhook_url")
	if err != nil || webhookURL == "" {
		logger.Warn("TEAMS_WEBHOOK_URL not configured — teams_send_message tool will fail until set")
	}
	p.webhookURL = webhookURL

	// Create webhook client with defaults (10s timeout, 3 retries).
	p.client = webhook.NewClient()

	// Register MCP transport for the teams_send_message tool.
	transport := NewTeamsTransport(p.client, p.webhookURL)
	mcpSvc, mcpErr := host.GetService("mcp")
	if mcpErr == nil {
		if mgr, ok := mcpSvc.(*mcp.Manager); ok {
			mgr.AddServer("teams", transport)
			logger.Info("teams_send_message registered as MCP server", "server", "teams")
		}
	} else {
		return fmt.Errorf("failed to get MCP manager: %w", mcpErr)
	}

	// Seed the Teams Connector agent profile if it doesn't exist.
	if err := seedAgent(host); err != nil {
		logger.Warn("failed to seed Teams Connector agent", "error", fmt.Sprintf("%v", err))
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("teams plugin loaded", "version", p.Version(), "webhook_configured", webhookURL != "")
	return nil
}

func (p *TeamsPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false

	// Remove MCP server on unload.
	if p.host != nil {
		mcpSvc, err := p.host.GetService("mcp")
		if err == nil {
			if mgr, ok := mcpSvc.(*mcp.Manager); ok {
				mgr.RemoveServer("teams")
			}
		}
		p.host.Logger().Info("teams plugin unloaded")
	}
	return nil
}

// Uninstall removes artifacts created by the plugin (agent profile).
func (p *TeamsPlugin) Uninstall(host plugin.Host) error {
	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("get store service: %w", err)
	}
	store, ok := svc.(interface {
		DeleteAgent(slug string) error
	})
	if !ok {
		return fmt.Errorf("store service does not support DeleteAgent")
	}
	if err := store.DeleteAgent("teams-connector"); err != nil {
		return fmt.Errorf("remove Teams Connector agent: %w", err)
	}
	host.Logger().Info("uninstalled teams plugin — removed Teams Connector agent profile")
	return nil
}

func (p *TeamsPlugin) Status() plugin.PluginStatus {
	return p.status
}
