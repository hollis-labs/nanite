package service

import (
	"github.com/hollis-labs/nanite/internal/contextbroker"
)

// Phase 0 item 21 ("Cut Modes, in full") retired this file's three tests —
// Test_ModeIsSessionAttribute_SameAgentDifferentModes_DifferentSlotContent,
// Test_ModeChangeMidSession_NextDispatchReflectsNewMode, and
// Test_ModeChangeMidSession_CacheableSlotsUnchanged — along with the whole
// Session Mode system they exercised (SP-20260512-0009 W5 / CW-20260512-0115,
// "mode is a SESSION attribute, not an agent attribute"). All three asserted
// that re-pointing sessions.current_mode_id produced different SlotMode
// content on the next dispatch; store.GetSessionMode/SetSessionMode and the
// modes table are gone, so there is no more session-mode pointer to swap.
//
// See internal/context/INVARIANTS.md's INV4 section for the full rationale
// and TASKS/phase-0/21-cut-modes.md for the task record. The load-bearing
// replacement for what these three tests covered —  SlotMode's position/
// identity survive, its content is now permanently empty, and it degrades
// through the same ActionSkip/skipped_no_content path as any other empty
// slot — is invariantModeSlotInert in slot_invariants_test.go, exercised
// across every dispatch flavor by TestSlotInvariants_AcrossDispatchTypes
// (chat, sync subagent, async subagent, background_agent), which is broader
// coverage than any single one of the three retired tests carried alone.
//
// findSlotDecision and indexOfSlot below are kept — slot_invariants_test.go
// still uses both directly.

// findSlotDecision returns the SlotDecision for the named slot, or a
// zero-value SlotDecision if the slot is absent. Callers test the returned
// SlotName for emptiness to detect absence.
func findSlotDecision(decisions []contextbroker.SlotDecision, name string) contextbroker.SlotDecision {
	for _, d := range decisions {
		if d.SlotName == name {
			return d
		}
	}
	return contextbroker.SlotDecision{}
}

// indexOfSlot returns the position of the named slot in the decisions slice,
// or -1 if absent.
func indexOfSlot(decisions []contextbroker.SlotDecision, name string) int {
	for i, d := range decisions {
		if d.SlotName == name {
			return i
		}
	}
	return -1
}
