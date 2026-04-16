package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/tool/intent"
)

// RegisterToolCacheCommand adds the `/tools` slash command to registry, wired
// to store. Accepted args (case-insensitive):
//
//	/tools         — show current session pin (on|off|auto)
//	/tools on      — force full-hydrate Tools slot for this session
//	/tools off     — force pointer-only Tools slot for this session
//	/tools auto    — clear any pin; resume classifier-driven state
//	/tools clear   — alias of auto
//
// The pin is ephemeral (per-process) — a process restart clears it.
func RegisterToolCacheCommand(registry *chat.CommandRegistry, store *toolCacheOverrideStore) {
	if registry == nil || store == nil {
		return
	}
	registry.Register(chat.SlashCommand{
		Name:        "tools",
		Description: "Pin the Tools slot to full (`on`) / pointer-only (`off`) / auto",
		Category:    "tools",
		Source:      "builtin",
		Args: []chat.CommandArg{
			{
				Name:        "mode",
				Description: "on | off | auto (clear)",
				Required:    false,
				Type:        "string",
				Options:     []string{"on", "off", "auto", "clear"},
			},
		},
	}, toolCacheCommandHandler(store))
}

func toolCacheCommandHandler(store *toolCacheOverrideStore) chat.CommandHandler {
	return func(_ context.Context, sessionID, args string) (*chat.CommandResult, error) {
		if sessionID == "" {
			return &chat.CommandResult{Action: "error", Content: "No active session."}, nil
		}
		arg := strings.ToLower(strings.TrimSpace(args))
		switch arg {
		case "", "status":
			return toolCacheStatusReply(store.Get(sessionID)), nil
		case "on":
			store.Set(sessionID, intent.OverrideOn)
			return &chat.CommandResult{Action: "message", Content: "Tools slot pinned **on** for this session — every turn hydrates full tool definitions."}, nil
		case "off":
			store.Set(sessionID, intent.OverrideOff)
			return &chat.CommandResult{Action: "message", Content: "Tools slot pinned **off** for this session — every turn stays at pointer-only."}, nil
		case "auto", "clear":
			store.Set(sessionID, intent.OverrideNone)
			return &chat.CommandResult{Action: "message", Content: "Tools slot pin cleared — classifier decides per-turn (the default)."}, nil
		default:
			return &chat.CommandResult{
				Action:  "error",
				Content: fmt.Sprintf("Unknown mode %q. Use `on`, `off`, or `auto`.", arg),
			}, nil
		}
	}
}

func toolCacheStatusReply(o intent.Override) *chat.CommandResult {
	var state string
	switch o {
	case intent.OverrideOn:
		state = "**on** (forced full hydration)"
	case intent.OverrideOff:
		state = "**off** (forced pointer-only)"
	default:
		state = "**auto** (classifier decides per-turn)"
	}
	return &chat.CommandResult{
		Action: "message",
		Content: "Tools slot state for this session: " + state +
			"\n\nUsage: `/tools on` · `/tools off` · `/tools auto`",
	}
}
