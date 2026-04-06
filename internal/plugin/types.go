package plugin

import "context"

// UISlotName identifies a named mount point in the Nanite frontend UI.
type UISlotName string

const (
	SlotNavRail            UISlotName = "nav-rail"
	SlotSettingsTab        UISlotName = "settings-tab"
	SlotRightRailTab       UISlotName = "right-rail-tab"
	SlotComposerToolbar    UISlotName = "composer-toolbar"
	SlotChatHeaderAction   UISlotName = "chat-header-action"
	SlotContextMenuMessage UISlotName = "context-menu:message"
	SlotContextMenuSession UISlotName = "context-menu:session"
	SlotCommandPalette     UISlotName = "command-palette"
	SlotComposerAbove      UISlotName = "composer-above"    // info drawers above composer input
	SlotComposerBelow      UISlotName = "composer-below"    // quick actions, suggestions below composer
	SlotMessageActions     UISlotName = "message-actions"    // per-message plugin buttons (translate, bookmark, etc.)
	SlotMessageHeader      UISlotName = "message-header"     // per-message badges/tags (sentiment, cost, etc.)
	SlotSessionSidebar     UISlotName = "session-sidebar"    // decorations on session list items
	SlotModal              UISlotName = "modal"              // plugin-triggered modal dialogs
)

// UISlotEntry is a single item registered into a UI slot by a plugin or core.
type UISlotEntry struct {
	ID        string                 `json:"id"`
	PluginID  string                 `json:"plugin_id"`          // plugin ID or "core"
	Slot      UISlotName             `json:"slot"`
	Label     string                 `json:"label"`
	Icon      string                 `json:"icon,omitempty"`     // Lucide icon name
	Priority  int                    `json:"priority,omitempty"` // higher = earlier in list
	Component string                 `json:"component,omitempty"` // frontend component name for full views
	Action    string                 `json:"action,omitempty"`   // action type: "navigate", "command", "modal", "handler"
	Props     map[string]interface{} `json:"props,omitempty"`    // arbitrary data passed to the frontend
}

// CommandArg defines a single argument for a slash command, used by the
// frontend to render autocomplete hints and validate input.
type CommandArg struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Type        string   `json:"type,omitempty"`    // "string", "select", "number", "boolean"
	Options     []string `json:"options,omitempty"` // for "select" type
}

// SlashCommandDef defines a slash command registered by a plugin.
type SlashCommandDef struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Category    string       `json:"category"`
	Args        []CommandArg `json:"args,omitempty"`               // structured argument schema for autocomplete
	Permission  string       `json:"required_permission,omitempty"` // optional permission gate (checked before execution)
	// Handler is called server-side when the command is executed.
	// ctx carries a timeout; sessionID is the active session; args is everything after the command name.
	// Return a map with "action" ("message"|"noop"|"error") and optional "content".
	Handler func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) `json:"-"`
}

// KeybindingDef defines a keyboard shortcut registered by a plugin.
// The frontend merges plugin-registered keybindings with core bindings.
type KeybindingDef struct {
	ID          string `json:"id"`          // unique identifier (e.g. "git.commit")
	Key         string `json:"key"`         // binding string: "mod+shift+k", "mod+g"
	Action      string `json:"action"`      // action type: "command" (slash cmd), "navigate", "handler"
	ActionValue string `json:"action_value"` // slash command name, page name, or handler ref
	Label       string `json:"label"`       // human-readable label (e.g. "Commit Changes")
	Description string `json:"description,omitempty"`
}
