package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	sdkplugin "github.com/hollis-labs/plugin-sdk"
	nplugin "github.com/hollis-labs/nanite/internal/plugin"
)

// CommandArg defines a single argument for a slash command (mirrors plugin.CommandArg).
type CommandArg struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Type        string   `json:"type,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// SlashCommand represents a slash command available in the chat UI.
type SlashCommand struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Category    string       `json:"category"`
	Source      string       `json:"source"`                         // "builtin" or plugin ID
	Args        []CommandArg `json:"args,omitempty"`                 // structured argument schema
	Permission  string       `json:"required_permission,omitempty"` // permission gate
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
		{SlashCommand{Name: "mode", Description: "Switch agent mode", Category: "agent", Source: "builtin"}, nil},
		{SlashCommand{Name: "memory", Description: "Browse and manage memories", Category: "tools", Source: "builtin"}, nil},
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

// RegisterPluginCommand registers a slash command from a plugin into the unified
// registry. Satisfies the plugin.CommandRegistrar interface.
//
// B.12: when the plugin handler returns an "envelopes" key carrying
// []sdkplugin.EnvelopeOut values (already filtered by B.11's strict validator
// at the subprocess emission site), each validated envelope is rendered as a
// fenced `nanite-envelope` block appended to the result content. Downstream
// consumers (frontend, chat.ParseEnvelopes) then pick them up through the
// existing envelope pipeline — no new transport is introduced.
func (r *CommandRegistry) RegisterPluginCommand(cmd nplugin.SlashCommandDef, source string) {
	var h CommandHandler
	if cmd.Handler != nil {
		h = func(ctx context.Context, sessionID, args string) (*CommandResult, error) {
			out, err := cmd.Handler(ctx, sessionID, args)
			if err != nil {
				return nil, err
			}
			action, _ := out["action"].(string)
			content, _ := out["content"].(string)
			content = appendPluginEnvelopeBlocks(content, out["envelopes"])
			// A command that emits only envelopes (no textual content and
			// action unset) should still surface to the chat as a rendered
			// message so the envelopes flow through the system-message
			// persistence path in handleExecuteCommand.
			if action == "" && content != "" {
				action = "message"
			}
			return &CommandResult{Action: action, Content: content}, nil
		}
	}

	// Convert plugin args to chat args.
	var cmdArgs []CommandArg
	for _, a := range cmd.Args {
		cmdArgs = append(cmdArgs, CommandArg{
			Name:        a.Name,
			Description: a.Description,
			Required:    a.Required,
			Type:        a.Type,
			Options:     a.Options,
		})
	}

	r.Register(SlashCommand{
		Name:        cmd.Name,
		Description: cmd.Description,
		Category:    cmd.Category,
		Source:      source,
		Args:        cmdArgs,
		Permission:  cmd.Permission,
	}, h)
}

// RemoveByPlugin drops every command whose Source equals pluginID and returns
// the count removed. Satisfies plugin.CommandRegistrar so the host's
// UnloadPlugin sweep can clear plugin-registered slash commands during
// hot-unload.
//
// Built-in commands ("builtin"), skill commands ("skill"), and file-based
// commands ("file") use reserved Source values that no plugin can assume,
// so they're safe from this sweep. An empty pluginID is a no-op — we never
// want to mass-delete commands that happen to have no source attribution.
func (r *CommandRegistry) RemoveByPlugin(pluginID string) int {
	if pluginID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for name, rc := range r.commands {
		if rc.Source == pluginID {
			delete(r.commands, name)
			n++
		}
	}
	return n
}

// RegisterSkillCommand registers a file-based skill as a slash command.
// Skills are server-side commands with category "skill".
func (r *CommandRegistry) RegisterSkillCommand(slug, name, description, argumentHint string) {
	cmd := SlashCommand{
		Name:        slug,
		Description: description,
		Category:    "skill",
		Source:      "file",
	}
	if argumentHint != "" {
		cmd.Args = []CommandArg{{
			Name:        "args",
			Description: argumentHint,
			Required:    false,
			Type:        "string",
		}}
	}

	// Skill commands use a "client" action — the frontend sends the skill
	// slug + args back via the normal message flow where the chat service
	// resolves and executes the skill.
	r.Register(cmd, func(_ context.Context, sessionID, args string) (*CommandResult, error) {
		return &CommandResult{
			Action:  "skill",
			Content: fmt.Sprintf("%s %s", slug, args),
		}, nil
	})
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

// appendPluginEnvelopeBlocks renders each validated plugin envelope into a
// fenced `nanite-envelope` block and appends it to content. The raw value is
// whatever lives at the "envelopes" key of the plugin command result map; it
// is either a `[]sdkplugin.EnvelopeOut` (from the subprocess path after B.11
// filtering) or a pre-marshaled `[]map[string]interface{}` (legacy shape).
// Any envelope whose payload cannot be marshaled is skipped without an error
// — the B.11 filter has already logged the fault and the absence of the
// rendered block is the visible failure mode.
func appendPluginEnvelopeBlocks(content string, raw any) string {
	if raw == nil {
		return content
	}
	typed, ok := raw.([]sdkplugin.EnvelopeOut)
	if !ok {
		// Tolerate the legacy map-shaped envelope payload. Everything else
		// is ignored; an unrecognized shape here indicates a plugin bug
		// that should already be flagged by the B.11 filter.
		items, ok := raw.([]map[string]interface{})
		if !ok {
			return content
		}
		for _, m := range items {
			t, _ := m["type"].(string)
			d, _ := m["data"].(map[string]interface{})
			typed = append(typed, sdkplugin.EnvelopeOut{Type: t, Data: d})
		}
	}
	var b strings.Builder
	b.WriteString(content)
	for _, env := range typed {
		block, err := renderEnvelopeBlock(env)
		if err != nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
	}
	return b.String()
}

// renderEnvelopeBlock formats a plugin-emitted EnvelopeOut as a
// `nanite-envelope` fenced block matching the chat.Envelope wire shape so
// chat.ParseEnvelopes can extract it downstream. The wire shape is kind=
// "envelope", version=1, type=<EnvelopeOut.Type>, data=<EnvelopeOut.Data>.
func renderEnvelopeBlock(env sdkplugin.EnvelopeOut) (string, error) {
	wire := map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    env.Type,
		"data":    env.Data,
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	return "```nanite-envelope\n" + string(payload) + "\n```", nil
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
