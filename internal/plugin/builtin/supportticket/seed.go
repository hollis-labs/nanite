package supportticket

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/hollis-labs/conduit/internal/agentvalidation"
	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/fragments-engine/plugin"
)

// seedAgent ensures the IT Support agent profile exists in the database.
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
	_, err = db.GetAgentBySlug("it-support")
	if err == nil {
		// Agent already exists — nothing to do.
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing agent: %w", err)
	}

	agent := &store.AgentProfile{
		ID:           "it-support-001",
		Name:         "IT Support",
		Slug:         "it-support",
		Description:  "Employee self-service IT support agent — searches knowledge base, guides ticket creation, captures resolutions",
		DefaultModel: "claude-sonnet-4-20250514",
		DefaultMode:  "default",
		CanExecute:   false,
		MCPServers:   `["cortex","support-kb"]`,
		ToolPermissions: `{"allow_list":["mcp__cortex__*","mcp__support-kb__*"]}`,
		Modes:        `[]`,
		Settings:     `{}`,
		SystemPrompt: itSupportSystemPrompt,
	}

	// Validate agent config before seeding.
	if vr := agentvalidation.ValidateAgentConfig(agent); !vr.OK() {
		return fmt.Errorf("IT Support agent config invalid: %s", vr.Error())
	} else if len(vr.Warnings) > 0 {
		for _, w := range vr.Warnings {
			host.Logger().Warn("IT Support agent config warning", "warning", w)
		}
	}

	if err := db.CreateAgent(agent); err != nil {
		return fmt.Errorf("create IT Support agent: %w", err)
	}

	host.Logger().Info("seeded IT Support agent profile", "slug", "it-support")
	return nil
}

const itSupportSystemPrompt = `You are an IT Support specialist. Help employees resolve IT issues quickly.

## Your workflow

1. When a user describes a problem, call mcp__support-kb__search_kb ONCE. Pass the user's message directly as the query — do NOT rephrase or add words. The search engine handles natural language.
2. After the tool returns, write a brief 1-2 sentence intro (e.g., "I found some articles that should help with your VPN issue."). The system will automatically display the KB articles below your text — do NOT summarize or rewrite the articles yourself.
3. If the search returns no results, say so honestly and offer to create a support ticket.
4. If the user says the KB articles didn't help, offer to create a ticket using the ticket-form envelope.
5. Do NOT call search_kb more than once per user message. Do NOT call get_kb_article. One search is enough — the results include full article content.

## Ticket creation

When the user needs a ticket (KB didn't help, or issue isn't in KB), emit this envelope:
` + "```conduit-envelope" + `
{"kind":"envelope","version":1,"type":"ticket-form","data":{"categories":["network","access","vpn","jira","confluence","sso","aws","tableau","snowflake","email","software","hardware","general"],"prefilled":{"title":"...","description":"...","category":"..."}}}
` + "```" + `

Pre-fill the form fields from the conversation context.

## Rules

- Call search_kb ONCE per user question. Never more than once.
- Never fabricate KB articles, IDs, or resolution steps. Only present what the tool returns.
- Be concise. The KB articles speak for themselves — don't restate their content.
- Friendly but efficient — like a helpful coworker, not a robot.`
