package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// RegisterServerCommands adds built-in server-side commands that need access
// to the store and provider registry. Called from NewEngine after the registry
// is created.
func (r *CommandRegistry) RegisterServerCommands(s *store.Store, providers *provider.Registry) {
	r.Register(SlashCommand{
		Name:        "status",
		Description: "Show session info (agent, model, tokens, cost)",
		Category:    "info",
		Source:      "builtin",
	}, statusHandler(s))

	r.Register(SlashCommand{
		Name:        "providers",
		Description: "List registered providers and their status",
		Category:    "config",
		Source:      "builtin",
	}, providersHandler(s, providers))

	r.Register(SlashCommand{
		Name:        "export",
		Description: "Export session as markdown",
		Category:    "session",
		Source:      "builtin",
	}, exportHandler(s))

	r.Register(SlashCommand{
		Name:        "search",
		Description: "Search messages across sessions",
		Category:    "search",
		Source:      "builtin",
		Args: []CommandArg{
			{Name: "query", Description: "Search term", Required: true, Type: "string"},
		},
	}, searchHandler(s))
}

func statusHandler(s *store.Store) CommandHandler {
	return func(_ context.Context, sessionID, _ string) (*CommandResult, error) {
		if sessionID == "" {
			return &CommandResult{Action: "error", Content: "No active session"}, nil
		}

		sess, err := s.GetSession(sessionID)
		if err != nil {
			return nil, fmt.Errorf("session not found: %w", err)
		}

		var b strings.Builder
		b.WriteString("**Session Status**\n\n")
		b.WriteString(fmt.Sprintf("**Session:** %s", sess.ShortCode))
		if sess.Title != "" {
			b.WriteString(fmt.Sprintf(" — %s", sess.Title))
		}
		b.WriteString("\n")

		if sess.Provider != "" {
			b.WriteString(fmt.Sprintf("**Provider:** %s\n", sess.Provider))
		}
		if sess.Model != "" {
			b.WriteString(fmt.Sprintf("**Model:** %s\n", sess.Model))
		}
		b.WriteString(fmt.Sprintf("**Status:** %s\n", sess.Status))
		b.WriteString(fmt.Sprintf("**Messages:** %d\n", sess.MessageCount))

		// Agent info.
		if sa, err := s.GetSessionPrimaryAgent(sessionID); err == nil {
			if agent, err := s.GetAgent(sa.AgentID); err == nil {
				b.WriteString(fmt.Sprintf("**Agent:** %s", agent.Name))
				if sa.Mode != "" {
					b.WriteString(fmt.Sprintf(" (mode: %s)", sa.Mode))
				}
				b.WriteString("\n")
			}
		}

		// Usage stats.
		if usage, err := s.GetSessionUsage(sessionID); err == nil && usage != nil {
			b.WriteString(fmt.Sprintf("\n**Token Usage**\n"))
			b.WriteString(fmt.Sprintf("  Input: %d | Output: %d | Total: %d\n",
				usage.InputTokens, usage.OutputTokens, usage.TotalTokens))
			if usage.CacheReadTokens > 0 || usage.CacheCreationTokens > 0 {
				b.WriteString(fmt.Sprintf("  Cache read: %d | Cache created: %d\n",
					usage.CacheReadTokens, usage.CacheCreationTokens))
			}
			if usage.EstimatedCostUSD > 0 {
				b.WriteString(fmt.Sprintf("  Estimated cost: $%.4f\n", usage.EstimatedCostUSD))
			}
		}

		b.WriteString(fmt.Sprintf("\n**Created:** %s\n", sess.CreatedAt))

		return &CommandResult{Action: "message", Content: b.String()}, nil
	}
}

func providersHandler(s *store.Store, reg *provider.Registry) CommandHandler {
	return func(_ context.Context, _ string, _ string) (*CommandResult, error) {
		var b strings.Builder
		b.WriteString("**Registered Providers**\n\n")

		// Runtime providers from the registry.
		names := reg.Names()
		if len(names) == 0 {
			b.WriteString("No providers registered.\n")
		} else {
			for _, name := range names {
				p, ok := reg.Get(name)
				if !ok {
					b.WriteString(fmt.Sprintf("- **%s** — not available\n", name))
					continue
				}
				caps := p.Capabilities()
				features := []string{}
				if caps.SupportsToolCalling {
					features = append(features, "tools")
				}
				if caps.SupportsImageInput {
					features = append(features, "images")
				}
				if caps.SupportsSystemPromptCaching {
					features = append(features, "caching")
				}
				if caps.SupportsStreamJSON {
					features = append(features, "stream-json")
				}
				featureStr := ""
				if len(features) > 0 {
					featureStr = fmt.Sprintf(" [%s]", strings.Join(features, ", "))
				}
				b.WriteString(fmt.Sprintf("- **%s**%s\n", name, featureStr))
			}
		}

		// DB-registered providers for additional context.
		if dbProviders, err := s.ListProviders(); err == nil && len(dbProviders) > 0 {
			b.WriteString(fmt.Sprintf("\n**Configured Providers** (%d total)\n", len(dbProviders)))
			for _, p := range dbProviders {
				status := "enabled"
				if !p.IsEnabled {
					status = "disabled"
				}
				b.WriteString(fmt.Sprintf("- %s (%s) — %s\n", p.Name, p.ProviderType, status))
			}
		}

		return &CommandResult{Action: "message", Content: b.String()}, nil
	}
}

func exportHandler(s *store.Store) CommandHandler {
	return func(_ context.Context, sessionID, args string) (*CommandResult, error) {
		if sessionID == "" {
			return &CommandResult{Action: "error", Content: "No active session"}, nil
		}

		sess, err := s.GetSession(sessionID)
		if err != nil {
			return nil, fmt.Errorf("session not found: %w", err)
		}

		msgs, err := s.ListMessages(sessionID, 10000)
		if err != nil {
			return nil, fmt.Errorf("failed to list messages: %w", err)
		}

		var b strings.Builder
		title := sess.Title
		if title == "" {
			title = "Untitled Session"
		}
		b.WriteString(fmt.Sprintf("# %s\n\n", title))
		b.WriteString(fmt.Sprintf("**Session:** %s | **Model:** %s | **Provider:** %s\n",
			sess.ShortCode, sess.Model, sess.Provider))
		b.WriteString(fmt.Sprintf("**Created:** %s | **Messages:** %d\n\n---\n\n",
			sess.CreatedAt, len(msgs)))

		for _, msg := range msgs {
			role := msg.Role
			switch role {
			case "user":
				role = "User"
			case "assistant":
				role = "Assistant"
			case "system":
				role = "System"
			case "tool":
				role = "Tool"
			}
			b.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", role, msg.Content))
		}

		return &CommandResult{Action: "message", Content: b.String()}, nil
	}
}

func searchHandler(s *store.Store) CommandHandler {
	return func(_ context.Context, sessionID, args string) (*CommandResult, error) {
		query := strings.TrimSpace(args)
		if query == "" {
			return &CommandResult{Action: "error", Content: "Usage: /search <query>"}, nil
		}

		// Get workspace from current session for scoping.
		workspaceID := ""
		if sessionID != "" {
			if sess, err := s.GetSession(sessionID); err == nil {
				workspaceID = sess.WorkspaceID
			}
		}

		results, err := s.SearchMessages(query, workspaceID, "", 20)
		if err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}

		if len(results) == 0 {
			return &CommandResult{Action: "message", Content: fmt.Sprintf("No results for **%s**", query)}, nil
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("**Search: \"%s\"** — %d result(s)\n\n", query, len(results)))
		for _, r := range results {
			title := r.SessionTitle
			if title == "" {
				title = r.SessionShortCode
			}
			b.WriteString(fmt.Sprintf("- **[%s]** %s — _%s_\n  > %s\n\n",
				r.Role, title, r.CreatedAt, r.Snippet))
		}

		return &CommandResult{Action: "message", Content: b.String()}, nil
	}
}
