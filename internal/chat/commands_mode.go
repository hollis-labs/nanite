package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// SessionModeSetter is the narrow surface the slash-mode commands need to
// flip session.current_mode_id (B1, CW-20260428-0009). Implemented by
// *store.Store. Kept as an interface so test doubles and future indirection
// (e.g. an audit-logging wrapper) can substitute without touching the
// command registry.
//
// B2 (CW-20260428-0010) — the new /mode, /chat, /plan, /work commands all
// route through this surface. The legacy POST /api/sessions/{id}/mode handler
// (handleSwitchSessionMode) was deliberately preserved by B1 because it
// targets the agent-scoped AgentMode, which is a separate concern from the
// session-level CurrentModeID introduced in B1.
type SessionModeSetter interface {
	GetModeBySlug(slug string) (*store.Mode, error)
	SetSessionMode(sessionID, modeID string) error
}

// RegisterModeCommands binds the /mode, /chat, /plan, /work slash commands
// to a SessionModeSetter. Called from container.go after NewCommandRegistry
// + RegisterServerCommands so the placeholder /mode entry from the registry
// constructor is replaced with the handler-bearing one.
//
// The /chat /plan /work commands are thin shortcuts that pre-fill the slug
// argument — they exist so the user can switch modes with a single keystroke
// path through the slash palette without typing "/mode <slug>".
//
// The "client" action carrying "mode_switched:<slug>" is the FE invalidation
// signal — useChat or ChatComposer listens for that exact prefix to
// invalidate the ['session-mode', sessionId] React Query so the chip
// refreshes immediately.
func (r *CommandRegistry) RegisterModeCommands(setter SessionModeSetter) {
	if setter == nil {
		return
	}

	handler := func(ctx context.Context, sessionID, args string) (*CommandResult, error) {
		return r.modeSwitchHandler(ctx, setter, sessionID, args)
	}

	// Replace the placeholder /mode entry with the handler-bearing one.
	r.Register(SlashCommand{
		Name:        "mode",
		Description: "Switch session mode (chat, plan, work, …)",
		Category:    "mode",
		Source:      "builtin",
		Args: []CommandArg{{
			Name:        "slug",
			Description: "Mode slug (chat, plan, work, …)",
			Required:    true,
			Type:        "string",
		}},
	}, handler)

	// Shortcuts: /chat, /plan, /work — slug baked in.
	for _, slug := range []string{"chat", "plan", "work"} {
		s := slug // capture for the closure below
		r.Register(SlashCommand{
			Name:        s,
			Description: fmt.Sprintf("Switch to %s mode", strings.ToUpper(s[:1])+s[1:]),
			Category:    "mode",
			Source:      "builtin",
		}, func(ctx context.Context, sessionID, _ string) (*CommandResult, error) {
			return r.modeSwitchHandler(ctx, setter, sessionID, s)
		})
	}
}

// modeSwitchHandler implements the shared switch logic for /mode and the
// /chat /plan /work shortcuts. Unknown slugs and store errors come back as
// "error" results so the FE surfaces them in chat without raising an
// exception. A successful switch returns action="client" with content
// "mode_switched:<slug>" — the FE listens for that prefix to invalidate
// session-mode queries.
func (r *CommandRegistry) modeSwitchHandler(_ context.Context, setter SessionModeSetter, sessionID, args string) (*CommandResult, error) {
	slug := strings.TrimSpace(args)
	if slug == "" {
		return &CommandResult{
			Action:  "message",
			Content: "Usage: `/mode <slug>` — try `/chat`, `/plan`, or `/work`.",
		}, nil
	}
	if sessionID == "" {
		return &CommandResult{Action: "error", Content: "No active session"}, nil
	}

	m, err := setter.GetModeBySlug(slug)
	if err != nil || m == nil {
		return &CommandResult{Action: "error", Content: fmt.Sprintf("Unknown mode: %q", slug)}, nil
	}
	if err := setter.SetSessionMode(sessionID, m.ID); err != nil {
		return &CommandResult{Action: "error", Content: err.Error()}, nil
	}
	return &CommandResult{Action: "client", Content: "mode_switched:" + slug}, nil
}
