package teams

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/fragments-engine/plugin"
)

// seedAgent ensures the Teams Connector agent profile exists in the database.
// It is idempotent — if the agent already exists, it does nothing.
func seedAgent(host plugin.Host) error {
	svc, err := host.GetService("store")
	if err != nil {
		return fmt.Errorf("get store service: %w", err)
	}

	db, ok := svc.(*store.Store)
	if !ok {
		return fmt.Errorf("store service is %T, expected *store.Store", svc)
	}

	// Check if the agent already exists.
	_, err = db.GetAgentBySlug("teams-connector")
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing agent: %w", err)
	}

	agent := &store.AgentProfile{
		ID:           "teams-connector-001",
		Name:         "Teams Connector",
		Slug:         "teams-connector",
		Description:  "Sends notifications and status updates to Microsoft Teams channels via Adaptive Cards",
		DefaultModel: "claude-sonnet-4-20250514",
		DefaultMode:  "default",
		CanExecute:   false,
		MCPServers:   `["teams"]`,
		ToolPermissions: `{"allow_list":["mcp__teams__*"]}`,
		Modes:        `[]`,
		Settings:     `{}`,
		SystemPrompt: teamsConnectorSystemPrompt,
	}

	if err := db.CreateAgent(agent); err != nil {
		return fmt.Errorf("create Teams Connector agent: %w", err)
	}

	host.Logger().Info("seeded Teams Connector agent profile", "slug", "teams-connector")
	return nil
}

const teamsConnectorSystemPrompt = `You are a Teams Connector agent. You send notifications and status updates to Microsoft Teams channels.

## Your workflow

1. When asked to notify a Teams channel, use the teams_send_message tool.
2. Provide a clear title and message. Include relevant facts as key-value pairs.
3. If a link is relevant, include it as link_url for a "View Details" button.

## Rules

- Always confirm what was sent after a successful delivery.
- If the webhook URL is not configured, tell the user to set TEAMS_WEBHOOK_URL.
- Keep messages concise — Teams cards have limited space.
- Use facts for structured data (status, priority, assignee, etc.).`
