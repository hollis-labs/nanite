package email

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/conduit/internal/mcp"
	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/fragments-engine/connectors/gmail"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("email", func() plugin.Plugin { return New() })
}

// EmailPlugin provides email integration via Gmail.
// When credentials are configured it uses the real Gmail API;
// otherwise it runs in demo mode with sample data.
type EmailPlugin struct {
	host     plugin.Host
	client   *gmail.Client // nil in demo mode
	demoMode bool
	status   plugin.PluginStatus
}

// New creates a new EmailPlugin instance.
func New() *EmailPlugin {
	return &EmailPlugin{}
}

func (p *EmailPlugin) ID() string          { return "email" }
func (p *EmailPlugin) Name() string        { return "Email (Gmail)" }
func (p *EmailPlugin) Version() string     { return "0.1.0" }
func (p *EmailPlugin) Description() string { return "Email integration — send and read email via Gmail" }
func (p *EmailPlugin) Dependencies() []string { return nil }

func (p *EmailPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Resolve Gmail credentials from config.
	credsFile, err := host.GetConfig("gmail_credentials_file")
	if err != nil || credsFile == "" {
		credsFile = "credentials.json"
	}
	tokenFile, err := host.GetConfig("gmail_token_file")
	if err != nil || tokenFile == "" {
		tokenFile = "gmail_token.json"
	}

	// Try to initialize the real Gmail client.
	ctx := host.Context()
	client, err := gmail.NewClient(ctx, gmail.Config{
		CredentialsFile: credsFile,
		TokenFile:       tokenFile,
	})
	if err != nil {
		logger.Warn("Gmail credentials not configured — running in demo mode", "error", fmt.Sprintf("%v", err))
		p.demoMode = true
	} else {
		p.client = client
		p.demoMode = false
	}

	// Register MCP tools via a transport.
	transport := &emailTransport{
		client:   p.client,
		demoMode: p.demoMode,
		logger:   logger,
	}

	mcpSvc, mcpErr := host.GetService("mcp")
	if mcpErr == nil {
		if mgr, ok := mcpSvc.(*mcp.Manager); ok {
			mgr.AddServer("email", transport)
			logger.Info("email tools registered as MCP server", "server", "email")
		}
	}

	// Register event hook (placeholder for future triggers).
	hook := &emailEventHook{logger: logger}
	if err := host.RegisterEventHook([]string{"message.sent"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	// Seed the Email Assistant agent profile.
	if err := seedEmailAgent(host); err != nil {
		logger.Warn("failed to seed Email Assistant agent", "error", fmt.Sprintf("%v", err))
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("email plugin loaded", "version", p.Version(), "demo_mode", p.demoMode)
	return nil
}

func (p *EmailPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("email plugin unloaded")
	}
	return nil
}

// Uninstall removes artifacts created by the plugin (agent profile).
func (p *EmailPlugin) Uninstall(host plugin.Host) error {
	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("get store service: %w", err)
	}
	db, ok := svc.(*store.Store)
	if !ok {
		return fmt.Errorf("store service is %T, expected *store.Store", svc)
	}
	if err := db.DeleteAgent("email-assistant"); err != nil {
		return fmt.Errorf("remove Email Assistant agent: %w", err)
	}
	host.Logger().Info("uninstalled email plugin — removed Email Assistant agent profile")
	return nil
}

func (p *EmailPlugin) Status() plugin.PluginStatus {
	return p.status
}

// emailEventHook listens for message.sent events (placeholder for future triggers).
type emailEventHook struct {
	logger plugin.Logger
}

func (h *emailEventHook) Handle(_ context.Context, event plugin.Event) error {
	h.logger.Debug("email: received event", "type", event.Type, "session", event.SessionID)
	return nil
}

func (h *emailEventHook) EventTypes() []string {
	return []string{"message.sent"}
}

// seedEmailAgent ensures the Email Assistant agent profile exists in the database.
func seedEmailAgent(host plugin.Host) error {
	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("get store service: %w", err)
	}

	db, ok := svc.(*store.Store)
	if !ok {
		return fmt.Errorf("store service is %T, expected *store.Store", svc)
	}

	// Check if the agent already exists.
	_, err = db.GetAgentBySlug("email-assistant")
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing agent: %w", err)
	}

	agent := &store.AgentProfile{
		ID:           "email-assistant-001",
		Name:         "Email Assistant",
		Slug:         "email-assistant",
		Description:  "Email assistant — send, read, and search email via Gmail",
		DefaultModel: "claude-sonnet-4-20250514",
		DefaultMode:  "default",
		CanExecute:   false,
		MCPServers:   `["email"]`,
		ToolPermissions: `{"allow":["mcp__email__*"]}`,
		Modes:        `[]`,
		Settings:     `{}`,
		SystemPrompt: emailAssistantSystemPrompt,
	}

	if err := db.CreateAgent(agent); err != nil {
		return fmt.Errorf("create Email Assistant agent: %w", err)
	}

	host.Logger().Info("seeded Email Assistant agent profile", "slug", "email-assistant")
	return nil
}

const emailAssistantSystemPrompt = `You are an Email Assistant. Help users read and send email via Gmail.

## Your tools

- **mcp__email__email_send** — Send an email. Takes to, subject, body, optional cc and is_html.
- **mcp__email__email_inbox** — List inbox messages. Takes optional query and max_results.
- **mcp__email__email_read** — Read a full email. Takes message_id.

## Workflow

1. When a user asks to check email, call email_inbox. The results will be displayed automatically.
2. When a user wants to read a specific email, call email_read with the message_id.
3. When a user wants to send an email, confirm the recipient, subject, and body before calling email_send.
4. Always confirm before sending — never send without explicit user approval.

## Rules

- Be concise. Email summaries are displayed as cards — don't repeat their content.
- When listing emails, let the inbox card do the display work.
- For composing, confirm all fields with the user before sending.
- If in demo mode, let the user know the data is simulated.`
