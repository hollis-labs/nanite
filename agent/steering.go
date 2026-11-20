package agent

import (
	"github.com/hollis-labs/libs/agent/steer"
	"github.com/hollis-labs/nanite/internal/engine"
)

// Steering configures libs agent/steer for the agent: the preset registry,
// the reflex vocabulary, the classifier policy, the work-slot selector, the
// host's work slots and reflexes, and an optional classifier. Only Registry
// is required.
type Steering = engine.Steering

// SteerVerdict is the classifier policy's ruling on one facet; a Steered
// event's suggestions are verdicts with outcome suggest.
type SteerVerdict = steer.Verdict

// GeneralSlot is the work slot in use when nothing more specific is, the
// default selector's fallback.
var GeneralSlot = engine.GeneralSlot

// ErrNoSteering is returned by the mode calls when the agent was opened
// without Steering.
var ErrNoSteering = engine.ErrNoSteering

// PreparedSteer is steering run ahead of a person's message by
// Conversation.Steer. Halted reports whether a reflex answered it; send the
// turn with it in Input.Steer.
type PreparedSteer = engine.PreparedSteer
