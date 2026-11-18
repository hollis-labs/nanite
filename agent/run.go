package agent

import (
	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/events"
)

// Run is an active conversation turn.
type Run = engine.Run

// EventSink receives normalized runtime events.
type EventSink = events.Sink

// Answer is part of the run event contract.
type Answer = events.Answer

// Event is part of the run event contract.
type Event = events.Event

// EventMeta is part of the run event contract.
type EventMeta = events.EventMeta

// Result is part of the run event contract.
type Result = events.Result

// RunStarted is part of the run event contract.
type RunStarted = events.RunStarted

// SlotsAssembled is part of the run event contract.
type SlotsAssembled = events.SlotsAssembled

// TextDelta is part of the run event contract.
type TextDelta = events.TextDelta

// Thinking is part of the run event contract.
type Thinking = events.Thinking

// ToolCallStarted is part of the run event contract.
type ToolCallStarted = events.ToolCallStarted

// PermissionAsked is part of the run event contract.
type PermissionAsked = events.PermissionAsked

// PermissionResolved is part of the run event contract.
type PermissionResolved = events.PermissionResolved

// ToolCallFinished is part of the run event contract.
type ToolCallFinished = events.ToolCallFinished

// Question is part of the run event contract.
type Question = events.Question

// SubagentStarted is part of the run event contract.
type SubagentStarted = events.SubagentStarted

// SubagentFinished is part of the run event contract.
type SubagentFinished = events.SubagentFinished

// Compacted is part of the run event contract.
type Compacted = events.Compacted

// Usage is part of the run event contract.
type Usage = events.Usage

// RunFinished is part of the run event contract.
type RunFinished = events.RunFinished

// RunFailed is part of the run event contract.
type RunFailed = events.RunFailed
