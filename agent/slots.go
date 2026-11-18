package agent

import (
	"fmt"
	"regexp"

	"github.com/hollis-labs/nanite/internal/slots"
)

// SlotName identifies a named context slot.
type SlotName = slots.Name

const (
	// SlotUniversal holds shared engine instructions.
	SlotUniversal = slots.Universal
	// SlotSystem holds host framing.
	SlotSystem = slots.System
	// SlotMemory holds recalled knowledge.
	SlotMemory = slots.Memory
	// SlotAgent holds the agent instructions.
	SlotAgent = slots.Agent
	// SlotMode holds the current role or mode.
	SlotMode = slots.ModeSlot
	// SlotRules holds rules and nudges.
	SlotRules = slots.Rules
	// SlotPermissions holds the permission summary.
	SlotPermissions = slots.Permissions
	// SlotWorkspace holds focused workspace information.
	SlotWorkspace = slots.Workspace
	// SlotSkills holds skill names and descriptions.
	SlotSkills = slots.Skills
	// SlotTools holds tool guidance.
	SlotTools = slots.Tools
	// SlotSession holds session facts and notices.
	SlotSession = slots.Session
	// SlotContext holds additional context.
	SlotContext = slots.Context
	// SlotUserContext holds the user's current context.
	SlotUserContext = slots.UserContext
	// SlotHandoff holds engine handoff information.
	SlotHandoff = slots.Handoff
	// SlotConversation is reserved for conversation accounting.
	SlotConversation = slots.Conversation
)

// SlotEntry is a single contribution to a slot.
type SlotEntry = slots.Entry

// SlotUpdate supplies a slot entry for one turn.
type SlotUpdate = slots.Update

// SlotSource supplies slot entries at the beginning of a turn.
type SlotSource = slots.Source

// SlotSelector decides what fits into the context.
type SlotSelector = slots.Selector

// SlotSnapshot is an immutable view of available entries.
type SlotSnapshot = slots.Snapshot

// SlotTurnInfo identifies the turn being assembled.
type SlotTurnInfo = slots.TurnInfo

// SlotReport describes assembled slots.
type SlotReport = slots.Report

// SlotDetail describes one assembled slot.
type SlotDetail = slots.SlotReport

// SlotPlan contains selector decisions.
type SlotPlan = slots.Plan

// SlotDecision determines how a slot entry is presented.
type SlotDecision = slots.Decision

// SlotModeValue is a selector decision mode.
type SlotModeValue = slots.Mode

const (
	// SlotShip ships the entry's full content.
	SlotShip = slots.ModeShip
	// SlotPointer ships its pointer.
	SlotPointer = slots.ModePointer
	// SlotSkip omits it.
	SlotSkip = slots.ModeSkip
)

// Slots manages host contributions to the next turn's context.
type Slots struct{ store *slots.Store }

// SlotWriter writes entries on behalf of one host owner.
type SlotWriter struct {
	store *slots.Store
	owner string
	err   error
}

var ownerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Owner returns a writer scoped to name. Mutations with an invalid name return an error.
func (s *Slots) Owner(name string) *SlotWriter {
	w := &SlotWriter{store: s.store, owner: name}
	if name == "engine" || name == "file" || !ownerPattern.MatchString(name) {
		w.err = fmt.Errorf("invalid host slot owner %q", name)
	}
	return w
}

// Put adds or updates one slot entry.
func (w *SlotWriter) Put(slot SlotName, e SlotEntry) error {
	if w.err != nil {
		return w.err
	}
	e.Owner = w.owner
	return w.store.Put(slot, e)
}

// Replace atomically replaces this owner's entries in a slot.
func (w *SlotWriter) Replace(slot SlotName, entries ...SlotEntry) error {
	if w.err != nil {
		return w.err
	}
	return w.store.Replace(slot, w.owner, entries...)
}

// Remove removes one entry belonging to this writer.
func (w *SlotWriter) Remove(slot SlotName, key string) {
	if w.err == nil {
		w.store.Remove(slot, w.owner, key)
	}
}

// Clear removes all contributions by this writer.
func (w *SlotWriter) Clear() {
	if w.err == nil {
		w.store.ClearOwner(w.owner)
	}
}
