package teams

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/conduit/internal/mcp"
	"github.com/hollis-labs/fragments-engine/connectors/webhook"
)

// TeamsTransport implements mcp.MCPTransport to provide the teams_send_message
// tool that agents can call to push Adaptive Cards to a Teams channel.
type TeamsTransport struct {
	client     *webhook.Client
	webhookURL string
}

// NewTeamsTransport creates a transport wrapping the webhook connector.
func NewTeamsTransport(client *webhook.Client, webhookURL string) *TeamsTransport {
	return &TeamsTransport{
		client:     client,
		webhookURL: webhookURL,
	}
}

// ListTools returns the teams_send_message tool definition.
func (t *TeamsTransport) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{
			Name:        "teams_send_message",
			Description: "Send an Adaptive Card message to a Microsoft Teams channel via Incoming Webhook. Use this to notify teams about events, share status updates, or send alerts.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Card title displayed as a large bold heading",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Main message body text",
					},
					"facts": map[string]any{
						"type":        "object",
						"description": "Optional key-value pairs displayed as a fact set (e.g., {\"Status\": \"Complete\", \"Priority\": \"High\"})",
						"additionalProperties": map[string]any{
							"type": "string",
						},
					},
					"link_url": map[string]any{
						"type":        "string",
						"description": "Optional URL to add as an action button labeled 'View Details'",
					},
				},
				"required": []string{"title", "message"},
			},
		},
	}, nil
}

// CallTool dispatches the teams_send_message tool.
func (t *TeamsTransport) CallTool(_ context.Context, name string, arguments map[string]any) (*mcp.ToolResult, error) {
	if name != "teams_send_message" {
		return &mcp.ToolResult{
			Content: []mcp.ToolContent{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", name)}},
			IsError: true,
		}, nil
	}

	return t.sendMessage(arguments)
}

func (t *TeamsTransport) sendMessage(args map[string]any) (*mcp.ToolResult, error) {
	if t.webhookURL == "" {
		return textResult("Error: TEAMS_WEBHOOK_URL is not configured. Set the environment variable or configure it in plugins/teams/plugin.yaml.", true), nil
	}

	title, _ := args["title"].(string)
	message, _ := args["message"].(string)
	if title == "" || message == "" {
		return textResult("Error: 'title' and 'message' parameters are required", true), nil
	}

	// Build the Adaptive Card.
	card := webhook.NewCard().Title(title).Text(message)

	// Add facts if provided.
	if factsRaw, ok := args["facts"]; ok {
		if factsMap, ok := factsRaw.(map[string]any); ok && len(factsMap) > 0 {
			facts := make(map[string]string, len(factsMap))
			for k, v := range factsMap {
				facts[k] = fmt.Sprintf("%v", v)
			}
			card.Facts(facts)
		}
	}

	// Add link action if provided.
	linkURL, _ := args["link_url"].(string)
	if linkURL != "" {
		card.Action("View Details", linkURL)
	}

	// Send to Teams.
	if err := t.client.SendTeams(t.webhookURL, card); err != nil {
		return textResult(fmt.Sprintf("Failed to send Teams message: %v", err), true), nil
	}

	// Build confirmation envelope for the chat UI.
	envData := map[string]any{
		"title":    title,
		"message":  message,
		"link_url": linkURL,
		"sent_at":  fmt.Sprintf("%v", args["sent_at"]),
	}
	if factsRaw, ok := args["facts"]; ok {
		envData["facts"] = factsRaw
	}

	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "teams-message",
		"data":    envData,
	})

	// Return confirmation text with embedded envelope for UI rendering.
	result := fmt.Sprintf("Message sent to Teams channel.\nTitle: %s\nMessage: %s", title, message) +
		"\n\n<!--ENVELOPE_DATA:" + string(envJSON) + ":ENVELOPE_DATA-->"

	return textResult(result, false), nil
}

// textResult is a convenience for building a single-text ToolResult.
func textResult(text string, isError bool) *mcp.ToolResult {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: text}},
		IsError: isError,
	}
}
