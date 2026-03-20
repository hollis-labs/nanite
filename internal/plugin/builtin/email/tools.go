package email

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/conduit/internal/mcp"
	"github.com/hollis-labs/fragments-engine/connectors/gmail"
	"github.com/hollis-labs/fragments-engine/plugin"
)

// emailTransport implements mcp.MCPTransport to provide email tools.
type emailTransport struct {
	client   *gmail.Client // nil when in demo mode
	demoMode bool
	logger   plugin.Logger
}

// ListTools returns all email tool definitions.
func (t *emailTransport) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{
			Name:        "email_send",
			Description: "Send an email via Gmail. Returns a confirmation envelope.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to": map[string]any{
						"type":        "string",
						"description": "Comma-separated recipient email addresses",
					},
					"subject": map[string]any{
						"type":        "string",
						"description": "Email subject line",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "Email body content",
					},
					"cc": map[string]any{
						"type":        "string",
						"description": "Comma-separated CC email addresses (optional)",
					},
					"is_html": map[string]any{
						"type":        "boolean",
						"description": "If true, body is sent as HTML (default false)",
					},
				},
				"required": []string{"to", "subject", "body"},
			},
		},
		{
			Name:        "email_inbox",
			Description: "List emails from Gmail inbox. Returns an inbox envelope with message summaries.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Gmail search query (e.g. 'is:unread', 'from:alice@example.com'). Default: 'is:inbox'",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of messages to return (default 10)",
					},
				},
			},
		},
		{
			Name:        "email_read",
			Description: "Read a single email by message ID. Returns an email preview envelope.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id": map[string]any{
						"type":        "string",
						"description": "Gmail message ID",
					},
				},
				"required": []string{"message_id"},
			},
		},
	}, nil
}

// CallTool dispatches email tool calls.
func (t *emailTransport) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolResult, error) {
	switch name {
	case "email_send":
		return t.handleSend(ctx, args)
	case "email_inbox":
		return t.handleInbox(ctx, args)
	case "email_read":
		return t.handleRead(ctx, args)
	default:
		return &mcp.ToolResult{
			Content: []mcp.ToolContent{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", name)}},
			IsError: true,
		}, nil
	}
}

// handleSend sends an email or simulates it in demo mode.
func (t *emailTransport) handleSend(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	to, _ := args["to"].(string)
	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)
	cc, _ := args["cc"].(string)
	isHTML, _ := args["is_html"].(bool)

	if to == "" || subject == "" || body == "" {
		return textResult("Error: 'to', 'subject', and 'body' are required", true), nil
	}

	toList := splitAndTrim(to)
	ccList := splitAndTrim(cc)

	if !t.demoMode && t.client != nil {
		err := t.client.SendEmail(ctx, gmail.SendOpts{
			To:      toList,
			CC:      ccList,
			Subject: subject,
			Body:    body,
			IsHTML:  isHTML,
		})
		if err != nil {
			return textResult(fmt.Sprintf("Failed to send email: %v", err), true), nil
		}
	}

	// Build envelope data.
	envData := map[string]any{
		"to":        to,
		"cc":        cc,
		"subject":   subject,
		"body":      truncate(body, 200),
		"is_html":   isHTML,
		"sent_at":   time.Now().Format(time.RFC3339),
		"demo_mode": t.demoMode,
	}
	envJSON, _ := json.Marshal(envData)

	modeLabel := ""
	if t.demoMode {
		modeLabel = " (demo mode — not actually sent)"
	}

	result := fmt.Sprintf("Email sent to %s%s", to, modeLabel) +
		"\n\n<!--ENVELOPE_DATA:{\"kind\":\"envelope\",\"version\":1,\"type\":\"email-compose\",\"data\":" + string(envJSON) + "}:ENVELOPE_DATA-->"

	return textResult(result, false), nil
}

// handleInbox lists inbox messages or returns demo data.
func (t *emailTransport) handleInbox(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	query, _ := args["query"].(string)
	if query == "" {
		query = "is:inbox"
	}
	maxResults := 10
	if mr, ok := args["max_results"].(float64); ok && mr > 0 {
		maxResults = int(mr)
	}

	var messages []emailSummary

	if t.demoMode || t.client == nil {
		messages = demoInbox(query, maxResults)
	} else {
		emails, err := t.client.ListMessages(ctx, query, maxResults)
		if err != nil {
			return textResult(fmt.Sprintf("Failed to list messages: %v", err), true), nil
		}
		for _, e := range emails {
			messages = append(messages, emailSummary{
				ID:      e.ID,
				From:    e.From,
				Subject: e.Subject,
				Snippet: e.Snippet,
				Date:    e.Date.Format(time.RFC3339),
				Labels:  e.Labels,
				Unread:  containsLabel(e.Labels, "UNREAD"),
			})
		}
	}

	envData := map[string]any{
		"query":      query,
		"messages":   messages,
		"total":      len(messages),
		"demo_mode":  t.demoMode,
	}
	envJSON, _ := json.Marshal(envData)

	// Compact summary for the LLM.
	summaryLines := make([]string, 0, len(messages))
	for _, m := range messages {
		unread := ""
		if m.Unread {
			unread = " [UNREAD]"
		}
		summaryLines = append(summaryLines, fmt.Sprintf("- %s: %s (from %s)%s", m.ID, m.Subject, m.From, unread))
	}

	modeLabel := ""
	if t.demoMode {
		modeLabel = " (demo mode)"
	}

	result := fmt.Sprintf("Found %d messages for query \"%s\"%s:\n%s", len(messages), query, modeLabel, strings.Join(summaryLines, "\n")) +
		"\n\n[SYSTEM: Inbox results will be displayed automatically. Write a brief summary.]" +
		"\n\n<!--ENVELOPE_DATA:{\"kind\":\"envelope\",\"version\":1,\"type\":\"email-inbox\",\"data\":" + string(envJSON) + "}:ENVELOPE_DATA-->"

	return textResult(result, false), nil
}

// handleRead fetches a single email or returns demo data.
func (t *emailTransport) handleRead(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	messageID, _ := args["message_id"].(string)
	if messageID == "" {
		return textResult("Error: 'message_id' is required", true), nil
	}

	var emailData map[string]any

	if t.demoMode || t.client == nil {
		email := demoGetMessage(messageID)
		if email == nil {
			return textResult(fmt.Sprintf("Message %s not found", messageID), true), nil
		}
		emailData = map[string]any{
			"id":        email.ID,
			"from":      email.From,
			"to":        email.To,
			"subject":   email.Subject,
			"date":      email.Date,
			"body":      email.Body,
			"labels":    email.Labels,
			"demo_mode": true,
		}
	} else {
		email, err := t.client.GetMessage(ctx, messageID)
		if err != nil {
			return textResult(fmt.Sprintf("Failed to read message: %v", err), true), nil
		}
		emailData = map[string]any{
			"id":        email.ID,
			"from":      email.From,
			"to":        email.To,
			"subject":   email.Subject,
			"date":      email.Date.Format(time.RFC3339),
			"body":      email.Body,
			"labels":    email.Labels,
			"demo_mode": false,
		}
	}

	envJSON, _ := json.Marshal(emailData)

	modeLabel := ""
	if t.demoMode {
		modeLabel = " (demo mode)"
	}

	sub, _ := emailData["subject"].(string)
	from, _ := emailData["from"].(string)

	result := fmt.Sprintf("Email: \"%s\" from %s%s", sub, from, modeLabel) +
		"\n\n<!--ENVELOPE_DATA:{\"kind\":\"envelope\",\"version\":1,\"type\":\"email-preview\",\"data\":" + string(envJSON) + "}:ENVELOPE_DATA-->"

	return textResult(result, false), nil
}

// --- Demo data ---

type emailSummary struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	Subject string   `json:"subject"`
	Snippet string   `json:"snippet"`
	Date    string   `json:"date"`
	Labels  []string `json:"labels"`
	Unread  bool     `json:"unread"`
}

type demoEmail struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Subject string   `json:"subject"`
	Date    string   `json:"date"`
	Body    string   `json:"body"`
	Labels  []string `json:"labels"`
	Snippet string   `json:"snippet"`
	Unread  bool     `json:"unread"`
}

var demoEmails = []demoEmail{
	{
		ID: "demo-001", From: "John Smith <john.smith@example.com>", To: "me@example.com",
		Subject: "Q1 Project Update", Date: "2026-03-20T09:15:00Z",
		Body:    "Hi,\n\nJust wanted to share a quick update on the Q1 project. We're on track to hit our milestones by end of month. The engineering team has completed the core API work and QA is running final regression tests.\n\nKey highlights:\n- API v2 shipped to staging\n- Performance benchmarks exceeded targets (sub-200ms p99)\n- 3 critical bugs fixed this sprint\n\nLet me know if you have any questions.\n\nBest,\nJohn",
		Snippet: "Just wanted to share a quick update on the Q1 project...",
		Labels:  []string{"INBOX", "UNREAD"}, Unread: true,
	},
	{
		ID: "demo-002", From: "HR Department <hr@example.com>", To: "all-staff@example.com",
		Subject: "Benefits Enrollment Deadline — March 31", Date: "2026-03-19T14:30:00Z",
		Body:    "Dear Team,\n\nThis is a reminder that the annual benefits enrollment period closes on March 31, 2026. Please review your current elections and make any changes through the HR portal.\n\nKey dates:\n- Enrollment closes: March 31\n- Changes effective: April 1\n- Info sessions: March 22 and 25 (see calendar invite)\n\nIf you have questions about your options, please reach out to benefits@example.com.\n\nThank you,\nHR Department",
		Snippet: "This is a reminder that the annual benefits enrollment period closes on March 31...",
		Labels:  []string{"INBOX"}, Unread: false,
	},
	{
		ID: "demo-003", From: "Sarah Chen <sarah.chen@example.com>", To: "me@example.com",
		Subject: "Re: Architecture Review Notes", Date: "2026-03-19T11:45:00Z",
		Body:    "Thanks for the detailed notes! I agree with the microservice boundary you proposed. Let's schedule a follow-up to discuss the event sourcing pattern for the order service.\n\nOne concern: the Redis dependency for session state might create a single point of failure. Have you considered using the existing Postgres instance with advisory locks instead?\n\nLet me know when you're free this week.\n\nSarah",
		Snippet: "Thanks for the detailed notes! I agree with the microservice boundary...",
		Labels:  []string{"INBOX", "UNREAD"}, Unread: true,
	},
	{
		ID: "demo-004", From: "GitHub <noreply@github.com>", To: "me@example.com",
		Subject: "[hollis-labs/conduit] PR #142: Add email plugin integration", Date: "2026-03-18T16:20:00Z",
		Body:    "A new pull request has been opened:\n\nPR #142: Add email plugin integration\nAuthor: @developer\nBranch: feature/email-plugin -> main\n\nThis PR adds the email plugin with Gmail integration, including send, inbox, and read tools with demo mode support.\n\n+847 -12 files changed\n\nReview requested.",
		Snippet: "A new pull request has been opened: PR #142: Add email plugin integration...",
		Labels:  []string{"INBOX"}, Unread: false,
	},
	{
		ID: "demo-005", From: "Alice Wong <alice.wong@example.com>", To: "me@example.com",
		Subject: "Lunch tomorrow?", Date: "2026-03-18T12:00:00Z",
		Body:    "Hey! Want to grab lunch tomorrow? I was thinking we could try that new Thai place on 5th Street. Let me know!\n\n- Alice",
		Snippet: "Hey! Want to grab lunch tomorrow? I was thinking we could try that new Thai place...",
		Labels:  []string{"INBOX", "UNREAD"}, Unread: true,
	},
}

func demoInbox(query string, maxResults int) []emailSummary {
	results := make([]emailSummary, 0, len(demoEmails))
	for _, e := range demoEmails {
		if len(results) >= maxResults {
			break
		}
		// Basic query filtering for demo.
		if query != "" && query != "is:inbox" {
			lower := strings.ToLower(query)
			if strings.Contains(lower, "is:unread") && !e.Unread {
				continue
			}
			if strings.HasPrefix(lower, "from:") {
				fromFilter := strings.TrimPrefix(lower, "from:")
				if !strings.Contains(strings.ToLower(e.From), strings.TrimSpace(fromFilter)) {
					continue
				}
			}
		}
		results = append(results, emailSummary{
			ID:      e.ID,
			From:    e.From,
			Subject: e.Subject,
			Snippet: e.Snippet,
			Date:    e.Date,
			Labels:  e.Labels,
			Unread:  e.Unread,
		})
	}
	return results
}

func demoGetMessage(id string) *demoEmail {
	for i := range demoEmails {
		if demoEmails[i].ID == id {
			return &demoEmails[i]
		}
	}
	return nil
}

// --- Helpers ---

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func containsLabel(labels []string, target string) bool {
	for _, l := range labels {
		if l == target {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func textResult(text string, isError bool) *mcp.ToolResult {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: text}},
		IsError: isError,
	}
}
