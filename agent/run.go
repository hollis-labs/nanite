package agent

import (
	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/events"
)

// Run is an active conversation turn. Read Events and call Answer for interactive
// questions before Wait. Wait declines any unanswered question it consumes.
type Run = engine.Run

// EventSink receives normalized runtime events.
type EventSink = events.Sink

// Answer carries a JSON value for a pending question. Use JSON null to decline.
type Answer = events.Answer

// Event is a sealed union of run lifecycle, content, tool, and interaction events.
// Use a type switch over the values read from Run.Events.
type Event = events.Event

// EventMeta identifies the originating run. Seq increases within one run stream.
type EventMeta = events.EventMeta

// Result contains the assistant text, stop reason, accumulated token usage, and
// number of completed model steps; for a dispatch, the outcome of each start.
type Result = events.Result

// Dispatched is the outcome of one start in a host Dispatch: the JSON the
// model's subtask call would have returned (a result contract, or a
// background job handle), or Err when it could not start.
type Dispatched = events.Dispatched

// NoticePosted reports a notice posted to a conversation. It arrives on the
// conversation's running turn, if any (EventMeta.RunID is that turn), and on
// every SubscribeNotices channel (RunID empty when no turn carried it).
type NoticePosted = events.NoticePosted

// RunStarted is emitted when the engine begins processing the turn.
type RunStarted = events.RunStarted

// SlotsAssembled reports the slot selection, token counts, and changes from the
// preceding turn before the model request.
type SlotsAssembled = events.SlotsAssembled

// TextDelta carries the next fragment of visible assistant text.
type TextDelta = events.TextDelta

// Thinking carries a completed reasoning fragment when the provider exposes one.
type Thinking = events.Thinking

// ToolCallStarted reports the model call ID, tool name, and redacted JSON arguments
// before authorization or execution.
type ToolCallStarted = events.ToolCallStarted

// PermissionAsked is emitted before Gate.Decide for a tool that needs approval.
type PermissionAsked = events.PermissionAsked

// PermissionResolved reports whether the tool may run and the decision reason.
// ID matches the original tool call.
type PermissionResolved = events.PermissionResolved

// ToolCallFinished reports the redacted result, error flag, and elapsed handler
// time. It can also report an unavailable or denied call.
type ToolCallFinished = events.ToolCallFinished

// Question is emitted when a tool pauses for user input. Use ID with Run.Answer
// and validate the response against the JSON Schema.
type Question = events.Question

// SubagentStarted identifies the child conversation and its variant.
type SubagentStarted = events.SubagentStarted

// SubagentFinished reports the result or error of a foreground child.
type SubagentFinished = events.SubagentFinished

// SubtaskProgress reports a running subtask's step, tool and status line,
// rate-limited per subtask. It is for the host; the model never sees it.
type SubtaskProgress = events.SubtaskProgress

// CompactionStarted reports that history compaction is beginning. Exactly one
// Compacted or CompactionFailed follows it.
type CompactionStarted = events.CompactionStarted

// CompactionFailed reports that compaction did not complete.
type CompactionFailed = events.CompactionFailed

// Compacted reports the token estimate before and after history compaction.
type Compacted = events.Compacted

// Usage reports a provider usage increment, including cache reads and writes.
type Usage = events.Usage

// RunFinished is the last event of a successful run and carries its final result.
type RunFinished = events.RunFinished

// RunFailed is the terminal event for a failed or cancelled run and carries
// the corresponding error.
type RunFailed = events.RunFailed
