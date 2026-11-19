package events

import (
	"encoding/json"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/tools"
)

type Sink = runtimeevents.Sink

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
	Report slots.Report
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
	Request permission.PermissionRequest
}

// PermissionResolved reports the tool permission decision.
type PermissionResolved struct {
	EventMeta
	ID       string
	Decision permission.PermissionDecision
}

// ToolCallFinished reports the outcome of a tool call.
type ToolCallFinished struct {
	EventMeta
	CallID   string
	Result   tools.Result
	IsError  bool
	Duration time.Duration
}

// Question requests a typed answer from the host.
type Question struct {
	EventMeta
	ID, Prompt string
	Schema     json.RawMessage
}

// SubagentStarted reports the launch of a subtask's child conversation.
type SubagentStarted struct {
	EventMeta
	ID, Variant, Goal string
	Background        bool
}

// SubagentFinished reports the outcome of a foreground subtask. Status is
// ok, partial, limit, error or cancelled. Limit is the child's stop reason
// when it stopped at a step, token, time or context limit before finishing;
// the child did not fail, but its work is incomplete.
type SubagentFinished struct {
	EventMeta
	ID, Variant string
	Status      string
	Result      Result
	Err         error
	Limit       string
}

// LimitReason returns stop when it reports that a turn stopped at a step,
// token or context limit, and "" for a finished turn.
func LimitReason(stop string) string {
	switch stop {
	case StopMaxSteps, StopTokenBudget, StopWallBudget, llmtypes.StopReasonMaxTokens, StopContextWindowExceeded:
		return stop
	}
	return ""
}

const (
	// StopMaxSteps is the stop reason of a turn that reached its step limit.
	StopMaxSteps = "max_steps"
	// StopTokenBudget is the stop reason of a subtask that spent its
	// variant's token budget.
	StopTokenBudget = "token_budget"
	// StopWallBudget is the stop reason of a subtask that ran past its
	// variant's wall-clock budget.
	StopWallBudget = "wall_time"
	// StopContextWindowExceeded is the provider stop reason for a response
	// cut off because the context window is full.
	StopContextWindowExceeded = "model_context_window_exceeded"
)

// CompactionStarted reports that the conversation history is about to be
// compacted. Exactly one Compacted or CompactionFailed follows it.
type CompactionStarted struct {
	EventMeta
	Before int
}

// CompactionFailed reports that compaction did not complete. The history is
// unchanged.
type CompactionFailed struct {
	EventMeta
	Err error
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
	// Subtask is set when this is a finished subtask's usage: the child
	// conversation's id.
	Subtask string
	// Background marks a background subtask's usage, reported on whatever
	// parent turn is running when the subtask finishes. It is not part of
	// that turn's total: summing a turn's Usage events without Background
	// gives the turn's total, foreground subtasks included.
	Background bool
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
