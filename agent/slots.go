package agent

import (
	"fmt"
	"regexp"

	"github.com/hollis-labs/nanite/internal/slots"
)

// SlotName identifies a named context slot.
type SlotName = slots.Name

const (
	// SlotUniversal holds shared engine instructions. Writes are reserved for Nanite.
	SlotUniversal = slots.Universal
	// SlotSystem holds host framing. Hosts may write this slot.
	SlotSystem = slots.System
	// SlotMemory holds recalled knowledge. Hosts may write this slot.
	SlotMemory = slots.Memory
	// SlotAgent holds the agent instructions. Writes are reserved for Nanite.
	SlotAgent = slots.Agent
	// SlotMode holds the current role or mode. Hosts may write this slot.
	SlotMode = slots.Mode
	// SlotRules holds rules and nudges. Hosts may write this slot.
	SlotRules = slots.Rules
	// SlotPermissions holds the permission summary. Writes are reserved for Nanite.
	SlotPermissions = slots.Permissions
	// SlotWorkspace holds focused workspace information. Hosts may write this slot.
	SlotWorkspace = slots.Workspace
	// SlotSkills holds skill names and descriptions. Hosts may write this slot.
	SlotSkills = slots.Skills
	// SlotTools holds tool guidance. Hosts may write this slot.
	SlotTools = slots.Tools
	// SlotSession holds session facts and notices. Hosts may write this slot.
	SlotSession = slots.Session
	// SlotContext holds additional context. Hosts may write this slot.
	SlotContext = slots.Context
	// SlotUserContext holds the user's current context. Hosts may write this slot.
	SlotUserContext = slots.UserContext
	// SlotHandoff holds engine handoff information. Writes are reserved for Nanite.
	SlotHandoff = slots.Handoff
	// SlotConversation holds conversation accounting. Writes are reserved for Nanite.
	SlotConversation = slots.Conversation
)

// SlotNames returns a fresh slice of all slot names in assembly order.
func SlotNames() []SlotName { return slots.Names() }

// SlotEntry is a contribution to a slot. Writers and sources overwrite Owner.
// A Key is unique per owner within its slot. Pointer is the one-line stand-in
// for the full content. Labels are never sent to the model.
type SlotEntry = slots.Entry

// SlotUpdate supplies a slot entry for one turn.
type SlotUpdate = slots.Update

// SlotSource supplies host-writable slot entries at the beginning of a turn.
// The source name becomes the owner of its entries.
type SlotSource = slots.Source

// SlotSelector uses the available entries and per-slot budgets to build a
// SlotPlan for the current turn.
type SlotSelector = slots.Selector

// SlotSnapshot is an immutable view of available entries.
type SlotSnapshot = slots.Snapshot

// SlotTurnInfo identifies the turn being assembled.
type SlotTurnInfo = slots.TurnInfo

// SlotReport describes assembled slots.
type SlotReport = slots.Report

// SlotDetail describes one assembled slot.
type SlotDetail = slots.SlotReport

// SlotPlan contains the selector decisions by slot. Entries omitted from the
// plan are skipped, except for the authored agent instructions.
type SlotPlan = slots.Plan

// SlotDecision determines how a slot entry is presented.
type SlotDecision = slots.Decision

// SlotDisposition determines whether a selector ships content, ships a pointer, or skips an entry.
type SlotDisposition = slots.Disposition

const (
	// SlotShip ships the entry's full content.
	SlotShip = slots.Ship
	// SlotPointer ships its pointer.
	SlotPointer = slots.Pointer
	// SlotSkip omits it.
	SlotSkip = slots.Skip
	// SlotTrim ships the start of its content, cut to fit what is left of the
	// slot's budget, with a marker. The default selector trims rather than
	// skips an entry that does not fit.
	SlotTrim = slots.Trim
)

// Slots manages host contributions to the next turn's context.
type Slots struct{ store *slots.Store }

// SlotWriter applies changes at the start of the next turn. A writer is safe
// for concurrent use.
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
