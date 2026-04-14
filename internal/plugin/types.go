package plugin

import (
	naniteplugin "github.com/hollis-labs/nanite/pkg/plugin"
)

// Re-export Nanite-specific UI plugin types so existing callers in this
// package keep compiling with unqualified names after the Phase 2 Track I
// split: universal types moved to github.com/hollis-labs/plugin-sdk, while
// host-specific UI types moved to github.com/hollis-labs/nanite/pkg/plugin.
type UISlotName = naniteplugin.UISlotName
type UISlotEntry = naniteplugin.UISlotEntry
type CommandArg = naniteplugin.CommandArg
type SlashCommandDef = naniteplugin.SlashCommandDef
type KeybindingDef = naniteplugin.KeybindingDef

// Re-exported slot constants defined in the Nanite public plugin package.
const (
	SlotNavRail            = naniteplugin.SlotNavRail
	SlotSettingsTab        = naniteplugin.SlotSettingsTab
	SlotRightRailTab       = naniteplugin.SlotRightRailTab
	SlotComposerToolbar    = naniteplugin.SlotComposerToolbar
	SlotChatHeaderAction   = naniteplugin.SlotChatHeaderAction
	SlotContextMenuMessage = naniteplugin.SlotContextMenuMessage
	SlotContextMenuSession = naniteplugin.SlotContextMenuSession
	SlotCommandPalette     = naniteplugin.SlotCommandPalette
	// Extra slot constants defined by Nanite for internal-only frontend
	// mount points not yet exposed to external plugins.
	SlotComposerAbove  UISlotName = "composer-above"
	SlotComposerBelow  UISlotName = "composer-below"
	SlotMessageActions UISlotName = "message-actions"
	SlotMessageHeader  UISlotName = "message-header"
	SlotSessionSidebar UISlotName = "session-sidebar"
	SlotModal          UISlotName = "modal"
)
