package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
)

// EventSink receives normalized runtime events.
type EventSink = runtimeevents.Sink

// Run is an active conversation turn.
type Run struct {
	id     string
	events <-chan Event
	cancel context.CancelFunc
}

// ID returns the turn's run identifier.
func (r *Run) ID() string { return r.id }

// Events returns the stream of turn events.
func (r *Run) Events() <-chan Event { return r.events }

// Wait waits for the terminal event and returns the turn result.
func (r *Run) Wait() (Result, error) { return Result{}, ErrNotImplemented }

// Answer responds to a question emitted by this run.
func (r *Run) Answer(string, Answer) error { return ErrNotImplemented }

// Cancel requests cancellation.
func (r *Run) Cancel() {
	if r.cancel != nil {
		r.cancel()
	}
}

// Answer contains a host's answer to a question.
type Answer struct{ Value json.RawMessage }

// Event is an event emitted by a run.
type Event interface{ event() }

// EventMeta identifies and orders events within a run.
type EventMeta struct {
	RunID string
	Seq   uint64
}

func (EventMeta) event() {}

// Result is the final output of a turn.
type Result struct {
	Text, StopReason string
	Usage            Usage
	Steps            int
}

// RunStarted marks the beginning of a run.
type RunStarted struct{ EventMeta }

// SlotsAssembled contains the slot assembly report.
type SlotsAssembled struct {
	EventMeta
	Report SlotReport
}

// TextDelta contains newly generated assistant text.
type TextDelta struct {
	EventMeta
	Text string
}

// Thinking contains newly generated reasoning text.
type Thinking struct {
	EventMeta
	Text string
}

// ToolCallStarted describes a requested tool call.
type ToolCallStarted struct {
	EventMeta
	CallID, Name string
	Input        json.RawMessage
}

// PermissionAsked records a tool call requiring human approval.
type PermissionAsked struct {
	EventMeta
	Request PermissionRequest
}

// PermissionResolved reports the tool permission decision.
type PermissionResolved struct {
	EventMeta
	ID       string
	Decision PermissionDecision
}

// ToolCallFinished reports the outcome of a tool call.
type ToolCallFinished struct {
	EventMeta
	CallID   string
	Result   ToolResult
	IsError  bool
	Duration time.Duration
}

// Question requests a typed answer from the host.
type Question struct {
	EventMeta
	ID, Prompt string
	Schema     json.RawMessage
}

// SubagentStarted reports the launch of a child conversation.
type SubagentStarted struct {
	EventMeta
	ID, Role string
}

// SubagentFinished reports the outcome of a child conversation.
type SubagentFinished struct {
	EventMeta
	ID     string
	Result Result
	Err    error
}

// Compacted reports the change in token usage after context compaction.
type Compacted struct {
	EventMeta
	Before, After int
}

// Usage reports model token use.
type Usage struct {
	EventMeta
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int
}

// RunFinished marks successful termination.
type RunFinished struct {
	EventMeta
	Result Result
}

// RunFailed marks unsuccessful termination.
type RunFailed struct {
	EventMeta
	Err error
}
