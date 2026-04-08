package plugin

import (
	goplugin "github.com/hollis-labs/go-plugin"
)

// Re-export SDK types so existing Nanite code keeps compiling without
// import-path changes outside this package.
type UISlotName = goplugin.UISlotName
type UISlotEntry = goplugin.UISlotEntry
type CommandArg = goplugin.CommandArg
type SlashCommandDef = goplugin.SlashCommandDef
type KeybindingDef = goplugin.KeybindingDef

// Extra slot constants defined by Nanite but not (yet) in the SDK.
const (
	SlotNavRail            = goplugin.SlotNavRail
	SlotSettingsTab        = goplugin.SlotSettingsTab
	SlotRightRailTab       = goplugin.SlotRightRailTab
	SlotComposerToolbar    = goplugin.SlotComposerToolbar
	SlotChatHeaderAction   = goplugin.SlotChatHeaderAction
	SlotContextMenuMessage = goplugin.SlotContextMenuMessage
	SlotContextMenuSession = goplugin.SlotContextMenuSession
	SlotCommandPalette     = goplugin.SlotCommandPalette
	SlotComposerAbove      UISlotName = "composer-above"    // info drawers above composer input
	SlotComposerBelow      UISlotName = "composer-below"    // quick actions, suggestions below composer
	SlotMessageActions     UISlotName = "message-actions"    // per-message plugin buttons (translate, bookmark, etc.)
	SlotMessageHeader      UISlotName = "message-header"     // per-message badges/tags (sentiment, cost, etc.)
	SlotSessionSidebar     UISlotName = "session-sidebar"    // decorations on session list items
	SlotModal              UISlotName = "modal"              // plugin-triggered modal dialogs
)
