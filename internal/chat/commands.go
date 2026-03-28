package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// SlashCommand represents a slash command available in the chat UI.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Source      string `json:"source"` // "builtin" or plugin ID
}

// CommandResult is the outcome of executing a slash command.
type CommandResult struct {
	// Action tells the frontend what to do with the result.
	// "message" = inject as system message, "noop" = command handled silently,
	// "client" = frontend should handle (e.g. open modal), "error" = show error.
	Action    string `json:"action"`
	Content   string `json:"content,omitempty"`
	MessageID string `json:"message_id,omitempty"` // set by API when message is persisted
}

// CommandHandler executes a slash command server-side.
type CommandHandler func(ctx context.Context, sessionID string, args string) (*CommandResult, error)

// CommandRegistry holds built-in and plugin-registered slash commands.
type CommandRegistry struct {
	mu       sync.RWMutex
	commands map[string]registeredCommand
}

type registeredCommand struct {
	SlashCommand
	handler CommandHandler // nil = client-side only
}

// NewCommandRegistry creates a registry pre-populated with built-in commands.
func NewCommandRegistry() *CommandRegistry {
	r := &CommandRegistry{
		commands: make(map[string]registeredCommand),
	}

	// Built-in commands — those with nil handler are dispatched client-side.
	builtins := []struct {
		cmd     SlashCommand
		handler CommandHandler
	}{
		{SlashCommand{Name: "new", Description: "Create new chat session", Category: "session", Source: "builtin"}, nil},
		{SlashCommand{Name: "fork", Description: "Fork current session with history", Category: "session", Source: "builtin"}, nil},
		{SlashCommand{Name: "clone", Description: "Clone session (empty)", Category: "session", Source: "builtin"}, nil},
		{SlashCommand{Name: "bookmark", Description: "Bookmark the last message", Category: "session", Source: "builtin"}, nil},
		{SlashCommand{Name: "compact", Description: "Compact session context", Category: "session", Source: "builtin"}, nil},
		{SlashCommand{Name: "agent", Description: "Switch primary agent", Category: "agent", Source: "builtin"}, nil},
		{SlashCommand{Name: "model", Description: "Switch model", Category: "config", Source: "builtin"}, nil},
		{SlashCommand{Name: "help", Description: "Show available commands", Category: "help", Source: "builtin"}, r.handleHelp},
	}

	for _, b := range builtins {
		r.commands[b.cmd.Name] = registeredCommand{SlashCommand: b.cmd, handler: b.handler}
	}
	return r
}

// handleHelp returns a formatted list of all available commands.
func (r *CommandRegistry) handleHelp(_ context.Context, _ string, _ string) (*CommandResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Group by category.
	groups := make(map[string][]SlashCommand)
	order := []string{}
	for _, rc := range r.commands {
		cat := rc.Category
		if _, exists := groups[cat]; !exists {
			order = append(order, cat)
		}
		groups[cat] = append(groups[cat], rc.SlashCommand)
	}

	var b strings.Builder
	b.WriteString("**Available Commands**\n\n")
	for _, cat := range order {
		title := cat
		if len(title) > 0 {
			title = strings.ToUpper(title[:1]) + title[1:]
		}
		b.WriteString(fmt.Sprintf("**%s**\n", title))
		for _, cmd := range groups[cat] {
			b.WriteString(fmt.Sprintf("  `/%s` — %s\n", cmd.Name, cmd.Description))
		}
		b.WriteString("\n")
	}

	return &CommandResult{Action: "message", Content: b.String()}, nil
}

// Register adds or replaces a slash command. Plugins call this via Host.RegisterCommand.
func (r *CommandRegistry) Register(cmd SlashCommand, handler CommandHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[cmd.Name] = registeredCommand{SlashCommand: cmd, handler: handler}
}

// List returns all registered commands.
func (r *CommandRegistry) List() []SlashCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SlashCommand, 0, len(r.commands))
	for _, rc := range r.commands {
		out = append(out, rc.SlashCommand)
	}
	return out
}

// Execute runs a command's server-side handler. Returns nil result if the
// command is client-side only (handler == nil).
func (r *CommandRegistry) Execute(ctx context.Context, name, sessionID, args string) (*CommandResult, error) {
	r.mu.RLock()
	rc, ok := r.commands[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown command: %s", name)
	}
	if rc.handler == nil {
		return &CommandResult{Action: "client", Content: name}, nil
	}
	return rc.handler(ctx, sessionID, args)
}

// ParseCommand extracts the command name and arguments from a slash command string.
// Input: "/agent claude-sonnet" → ("agent", "claude-sonnet")
func ParseCommand(input string) (name, args string) {
	input = strings.TrimPrefix(input, "/")
	parts := strings.SplitN(input, " ", 2)
	name = parts[0]
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return
}
