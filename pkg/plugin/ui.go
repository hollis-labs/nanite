// Package plugin exposes Nanite's host-specific extensions to the universal
// plugin contract defined in github.com/hollis-labs/plugin-sdk.
//
// The SDK in plugin-sdk is intentionally host-agnostic. Nanite layers on UI
// primitives (slot mount points, slash commands, keybindings) that only make
// sense for the Nanite desktop frontend. External plugins that want to target
// those surfaces import this package alongside the SDK.
//
// This package was introduced during Phase 2 Track I when the legacy
// github.com/hollis-labs/go-plugin module was retired and its UI surface
// split out of the SDK into this Nanite-owned package.
package plugin

import "context"

// UISlotName identifies a named mount point in the frontend UI.
type UISlotName string

// Built-in slot names the frontend knows how to render. Plugins can register
// entries into any of these by calling Host.RegisterSlot.
const (
	SlotNavRail            UISlotName = "nav-rail"
	SlotSettingsTab        UISlotName = "settings-tab"
	SlotRightRailTab       UISlotName = "right-rail-tab"
	SlotComposerToolbar    UISlotName = "composer-toolbar"
	SlotChatHeaderAction   UISlotName = "chat-header-action"
	SlotContextMenuMessage UISlotName = "context-menu:message"
	SlotContextMenuSession UISlotName = "context-menu:session"
	SlotCommandPalette     UISlotName = "command-palette"
)

// UISlotEntry is a single item registered into a UI slot by a plugin or core.
type UISlotEntry struct {
	ID        string                 `json:"id"`
	PluginID  string                 `json:"plugin_id"`
	Slot      UISlotName             `json:"slot"`
	Label     string                 `json:"label"`
	Icon      string                 `json:"icon,omitempty"`
	Priority  int                    `json:"priority,omitempty"`
	Component string                 `json:"component,omitempty"`
	Action    string                 `json:"action,omitempty"`
	Props     map[string]interface{} `json:"props,omitempty"`
}

// CommandArg defines a single argument for a slash command. The frontend uses
// this to render autocomplete hints and validate input.
type CommandArg struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Type        string   `json:"type,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// SlashCommandDef defines a slash command registered by a plugin.
type SlashCommandDef struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Category    string       `json:"category"`
	Args        []CommandArg `json:"args,omitempty"`
	Permission  string       `json:"required_permission,omitempty"`
	// Handler is called server-side when the command is executed. Return a
	// map with "action" ("message"|"noop"|"error") and optional "content".
	Handler func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) `json:"-"`
}

// KeybindingDef defines a keyboard shortcut registered by a plugin. The
// frontend merges plugin-registered keybindings with core bindings.
type KeybindingDef struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Action      string `json:"action"`
	ActionValue string `json:"action_value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}
