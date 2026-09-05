package agent

import (
	"context"
	"encoding/json"
	"sync"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

// runtimeEventSink implements runtimeevents.Sink, the seam
// wrapper.Config.Activity's Bridge writes every emitted runtime event to.
//
// wrapper.Wrapper.Run owns the raw llmtypes.StreamEvent / provider/events
// consumption internally (agent.go pre-migration fed those two surfaces
// straight to Dependencies.EventFanout / Dependencies.TypedEventCallback;
// Wrapper.Run now consumes them itself and re-emits a normalized
// runtimeevents.Event stream instead). This Sink first writes that complete
// normalized contract to the canonical sink, then projects the subset with
// legacy equivalents back onto the two surfaces
// internal/service/agentEventBridge already knows how to turn into chat SSE
// (delta / tool_call / tool_result / stream_end / error). See
// go-agent-wrapper's wrapper/event_translator.go for the forward mapping the
// compatibility projection reverses.
//
// Also doubles as the Boot-time readiness signal: wrapper.Wrapper.Run sets
// its internal session handle immediately before emitting
// runtimeevents.KindSessionReady (unconditionally, every runtime kind), so
// observing that Kind here is the one point at which SendInput/Stop become
// safe to call on the *wrapper.Wrapper — Boot blocks on it (via onReady)
// before returning a *Session to its own caller.
type runtimeEventSink struct {
	// canonical receives the complete normalized contract before the
	// compatibility projection below. It preserves lifecycle, process, raw,
	// permission, interrupt, and future/unknown kinds that have no legacy
	// llmtypes/provider equivalent. It is deliberately not an SSE surface.
	canonical runtimeevents.Sink
	fanout    chan<- llmtypes.StreamEvent
	typedCB   provider.EventsCallback
	acp       bool

	readyOnce sync.Once
	onReady   func()
}

var _ runtimeevents.Sink = (*runtimeEventSink)(nil)

// Write implements runtimeevents.Sink. Invoked synchronously from
// wrapper.Wrapper.Run's translator goroutine (per runtimeevents.Sink's own
// doc contract) — never blocks indefinitely: channel sends respect ctx and
// silently drop on cancellation, matching Sink.Write's "drop on overflow
// without erroring" guidance.
func (s *runtimeEventSink) Write(ctx context.Context, ev runtimeevents.Event) error {
	var canonicalErr error
	if s.canonical != nil {
		canonicalErr = s.canonical.Write(ctx, ev)
	}
	if ev.Kind == runtimeevents.KindSessionReady {
		s.signalReady()
	}

	switch ev.Kind {
	case runtimeevents.KindAgentDelta:
		s.handleDelta(ctx, ev.Payload)
	case runtimeevents.KindAgentToolUse:
		s.handleToolUse(ev.Payload)
	case runtimeevents.KindAgentToolResult:
		s.handleToolResult(ev.Payload)
	case runtimeevents.KindAgentSubagentSpawn:
		s.handleSubagentSpawn(ev.Payload)
	case runtimeevents.KindTurnCompleted:
		s.handleTurnCompleted(ctx, ev.Payload)
	case runtimeevents.KindTurnFailed:
		s.handleTurnFailed(ctx, ev.Payload)
	default:
		// No legacy equivalent. The exact event already reached canonical;
		// raw/unknown kinds stay internal until CW-20260904-0129 defines a
		// public transport contract.
	}
	return canonicalErr
}

func (s *runtimeEventSink) signalReady() {
	if s.onReady == nil {
		return
	}
	s.readyOnce.Do(s.onReady)
}

func (s *runtimeEventSink) sendFanout(ctx context.Context, ev llmtypes.StreamEvent) {
	if s.fanout == nil {
		return
	}
	select {
	case s.fanout <- ev:
	case <-ctx.Done():
	}
}

// deltaPayload mirrors translateStreamEvent's two llmtypes.StreamEvent ->
// KindAgentDelta shapes: {"content": ev.Content} for EventDelta and
// {"thinking": ev.ThinkingBlock} for EventThinking.
type deltaPayload struct {
	Content  string          `json:"content"`
	Phase    string          `json:"phase"`
	Thinking json.RawMessage `json:"thinking"`
}

type thinkingPayload struct {
	Thinking  string `json:"Thinking"`
	Signature string `json:"Signature"`
}

func (s *runtimeEventSink) handleDelta(ctx context.Context, raw json.RawMessage) {
	var p deltaPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	if len(p.Thinking) > 0 && string(p.Thinking) != "false" && string(p.Thinking) != "null" {
		var thinking thinkingPayload
		if p.Thinking[0] == '{' {
			_ = json.Unmarshal(p.Thinking, &thinking)
		}
		if thinking.Thinking == "" {
			thinking.Thinking = p.Content
		}
		s.sendFanout(ctx, llmtypes.StreamEvent{
			Type: llmtypes.EventThinking,
			ThinkingBlock: &llmtypes.ThinkingBlock{
				Thinking:  thinking.Thinking,
				Signature: thinking.Signature,
			},
		})
		return
	}
	if p.Phase == "thought" {
		s.sendFanout(ctx, llmtypes.StreamEvent{
			Type:          llmtypes.EventThinking,
			ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: p.Content},
		})
		return
	}
	s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: p.Content})
}

// toolUsePayload mirrors translateStreamEvent's {"tool_use": ev.ToolUse}
// shape, where ev.ToolUse is *llmtypes.ToolUseBlock{ID,Name,Input}.
type toolUsePayload struct {
	ToolUse *struct {
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	} `json:"tool_use"`
}

// handleToolUse drives the SAME surface pre-migration tool_call SSE came
// from (TypedEventCallback's events.ToolUse case) rather than the
// EventFanout llmtypes.EventToolUse case, which agentEventBridge
// deliberately drops today ("also surface via TypedEventCallback... skip
// here to avoid double-emission"). wrapper.Wrapper's own translation only
// reaches KindAgentToolUse via the EventFanout / llmtypes.EventToolUse
// path (translateProviderEvent never forwards provider/events.ToolUse) —
// reconstructing it onto TypedEventCallback here, rather than replaying it
// back onto EventFanout, is what makes the chat tool_call SSE actually
// fire again post-migration.
func (s *runtimeEventSink) handleToolUse(raw json.RawMessage) {
	if s.typedCB == nil || len(raw) == 0 {
		return
	}
	var p toolUsePayload
	if err := json.Unmarshal(raw, &p); err == nil && p.ToolUse != nil {
		s.typedCB(events.ToolUse{
			ID:   p.ToolUse.ID,
			Name: p.ToolUse.Name,
			Args: p.ToolUse.Input,
		})
		return
	}
	var flat struct {
		ToolCallID string          `json:"tool_call_id"`
		Name       string          `json:"name"`
		Title      string          `json:"title"`
		RawInput   json.RawMessage `json:"raw_input"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil || flat.ToolCallID == "" {
		return
	}
	var input map[string]any
	_ = json.Unmarshal(flat.RawInput, &input)
	s.typedCB(events.ToolUse{
		ID:   flat.ToolCallID,
		Name: firstNonEmpty(flat.Name, flat.Title),
		Args: input,
	})
}

// toolResultPayload mirrors translateProviderEvent's
// {"tool_result": {"id","is_error","content_preview"}} shape.
type toolResultPayload struct {
	ToolResult *struct {
		ID             string `json:"id"`
		IsError        bool   `json:"is_error"`
		ContentPreview string `json:"content_preview"`
	} `json:"tool_result"`
}

func (s *runtimeEventSink) handleToolResult(raw json.RawMessage) {
	if s.typedCB == nil || len(raw) == 0 {
		return
	}
	var p toolResultPayload
	if err := json.Unmarshal(raw, &p); err == nil && p.ToolResult != nil {
		s.typedCB(events.ToolResult{
			ID:             p.ToolResult.ID,
			IsError:        p.ToolResult.IsError,
			ContentPreview: p.ToolResult.ContentPreview,
		})
		return
	}
	var flat struct {
		ToolCallID string `json:"tool_call_id"`
		IsError    bool   `json:"is_error"`
		Result     any    `json:"result"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil || flat.ToolCallID == "" {
		return
	}
	preview := ""
	if flat.Result != nil {
		if encoded, err := json.Marshal(flat.Result); err == nil {
			preview = string(encoded)
		}
	}
	s.typedCB(events.ToolResult{
		ID:             flat.ToolCallID,
		IsError:        flat.IsError,
		ContentPreview: preview,
	})
}

// subagentSpawnPayload mirrors translateProviderEvent's
// {"subagent_spawn": {"tool","args"}} shape.
type subagentSpawnPayload struct {
	SubagentSpawn *struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	} `json:"subagent_spawn"`
}

func (s *runtimeEventSink) handleSubagentSpawn(raw json.RawMessage) {
	if s.typedCB == nil || len(raw) == 0 {
		return
	}
	var p subagentSpawnPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.SubagentSpawn == nil {
		return
	}
	s.typedCB(events.SubagentSpawn{Tool: p.SubagentSpawn.Tool, Args: p.SubagentSpawn.Args})
}

// turnCompletedPayload mirrors translateStreamEvent's two
// llmtypes.StreamEvent -> KindTurnCompleted shapes: {"usage": ev.Usage}
// for EventUsage and a nil/empty payload for EventDone.
type turnCompletedPayload struct {
	Usage *llmtypes.Usage `json:"usage"`
}

func (s *runtimeEventSink) handleTurnCompleted(ctx context.Context, raw json.RawMessage) {
	if len(raw) == 0 {
		s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDone})
		return
	}
	var p turnCompletedPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Usage == nil {
		s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDone})
		return
	}
	s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: p.Usage})
	if s.acp {
		// ACP reports usage and terminal completion together in one event;
		// native adapters emit a second empty KindTurnCompleted event.
		s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDone})
	}
	// llmtypes.EventUsage and llmtypes.EventDone arrive as two distinct
	// StreamEvents pre-migration (parseCodexStreamLine's turn.completed
	// case emits both when usage is present) — translateStreamEvent maps
	// each individually to its own KindTurnCompleted Write call, so a
	// usage-bearing Write here never also carries the terminal Done
	// signal; the adapter's own separate nil-payload KindTurnCompleted
	// Write (from the paired EventDone) supplies that.
}

// turnFailedPayload mirrors translateStreamEvent's {"error": ev.Error}
// shape for llmtypes.EventError.
type turnFailedPayload struct {
	Error string `json:"error"`
}

func (s *runtimeEventSink) handleTurnFailed(ctx context.Context, raw json.RawMessage) {
	var p turnFailedPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: p.Error})
}
