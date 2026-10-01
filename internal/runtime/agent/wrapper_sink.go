package agent

import (
	"context"
	"encoding/json"
	"strings"
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
// runtimeevents.Event stream instead). When a canonical sink is configured,
// this Sink first writes that complete normalized contract there, then
// projects the subset with legacy equivalents back onto the two surfaces
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

	// Block separation state (see separateBlocks).
	blockMu     sync.Mutex
	textInTurn  bool   // a text delta has been seen in the current turn
	textAtBreak bool   // that text ended in a newline
	textBlockID string // block_id of the turn's last text delta

	readyOnce sync.Once
	onReady   func()
}

var _ runtimeevents.Sink = (*runtimeEventSink)(nil)

// legacyStreamProjectionOwner marks a canonical sink that also owns the
// llmtypes stream projection. The service bridge uses this to preserve
// normalized TurnID ownership through terminal delivery; arbitrary injected
// canonical sinks do not satisfy it and retain the normal legacy projection.
type legacyStreamProjectionOwner interface {
	OwnsLegacyStreamProjection()
}

// Write implements runtimeevents.Sink. Invoked synchronously from
// wrapper.Wrapper.Run's translator goroutine (per runtimeevents.Sink's own
// doc contract) — never blocks indefinitely: channel sends respect ctx and
// silently drop on cancellation, matching Sink.Write's "drop on overflow
// without erroring" guidance.
func (s *runtimeEventSink) Write(ctx context.Context, ev runtimeevents.Event) error {
	ev = s.separateBlocks(ev)
	var canonicalErr error
	if s.canonical != nil {
		canonicalErr = s.canonical.Write(ctx, ev)
	}
	if ev.Kind == runtimeevents.KindSessionReady {
		s.signalReady()
	}

	_, canonicalOwnsStream := s.canonical.(legacyStreamProjectionOwner)
	switch ev.Kind {
	case runtimeevents.KindAgentDelta:
		if !canonicalOwnsStream {
			s.handleDelta(ctx, ev.Payload)
		}
	case runtimeevents.KindAgentToolUse:
		s.handleToolUse(ev.Payload)
	case runtimeevents.KindAgentToolResult:
		s.handleToolResult(ev.Payload)
	case runtimeevents.KindAgentSubagentSpawn:
		s.handleSubagentSpawn(ev.Payload)
	case runtimeevents.KindSessionLost, runtimeevents.KindSessionAuthFailed, runtimeevents.KindAgentPermissionDenied:
		s.handleSessionNotice(ev.Kind, ev.Payload)
	case runtimeevents.KindTurnCompleted:
		if !canonicalOwnsStream {
			s.handleTurnCompleted(ctx, ev.Payload)
		}
	case runtimeevents.KindTurnFailed:
		if !canonicalOwnsStream {
			s.handleTurnFailed(ctx, ev.Payload)
		}
	default:
		// No legacy equivalent. The exact event already reached canonical;
		// raw/unknown kinds stay internal until CW-20260904-0129 defines a
		// public transport contract.
	}
	return canonicalErr
}

// newRuntimeEventSink builds the sink for one Boot. Block separation applies
// to native runtimes only: an ACP session streams token chunks whatever the
// provider.
func newRuntimeEventSink(providerName string, isACP bool, canonical runtimeevents.Sink) *runtimeEventSink {
	return &runtimeEventSink{
		acp:       isACP,
		canonical: canonical,
	}
}

// separateBlocks puts a paragraph break between two content blocks of the
// same turn, unless the join is already at a line break. A block boundary is
// a change of the delta's block_id: since go-agent-wrapper v0.17.0 every
// runtime stamps it (Claude's event uuid, codex exec's item.id, opencode's
// part id, ACP's messageId), and every token of one block carries the same
// one. A delta with no block_id gets no break, so a runtime that cannot say
// which block a fragment belongs to keeps its text joined, as token streams
// must be. This replaced the per-provider deltasAreWholeBlocks stopgap
// (CW-20260930-0228).
//
// The break is written into the event itself, so every consumer — the
// canonical sink, the runtime feed, the chat stream, drain paths — sees the
// same text. Thinking deltas neither receive nor count as text.
//
// Every KindTurnCompleted ends the turn. Since go-agent-wrapper v0.13.1 a
// native turn has exactly one, its terminal, carrying the turn's usage.
func (s *runtimeEventSink) separateBlocks(ev runtimeevents.Event) runtimeevents.Event {
	s.blockMu.Lock()
	defer s.blockMu.Unlock()

	switch ev.Kind {
	case runtimeevents.KindTurnStarted, runtimeevents.KindTurnCompleted, runtimeevents.KindTurnFailed:
		s.textInTurn = false
		s.textBlockID = ""
		return ev
	case runtimeevents.KindAgentDelta:
	default:
		return ev
	}

	var fields map[string]json.RawMessage
	if len(ev.Payload) == 0 || json.Unmarshal(ev.Payload, &fields) != nil {
		return ev
	}
	var p deltaPayload
	_ = json.Unmarshal(ev.Payload, &p)
	if p.Phase == "thought" || (len(p.Thinking) > 0 && string(p.Thinking) != "false" && string(p.Thinking) != "null") {
		return ev
	}
	if p.Content == "" {
		return ev
	}

	content := p.Content
	newBlock := p.BlockID != "" && p.BlockID != s.textBlockID
	if s.textInTurn && newBlock && !s.textAtBreak && !startsWithSpace(content) {
		content = "\n\n" + content
		encoded, err := json.Marshal(content)
		if err != nil {
			return ev
		}
		fields["content"] = encoded
		payload, err := json.Marshal(fields)
		if err != nil {
			return ev
		}
		ev.Payload = payload
	}
	s.textInTurn = true
	s.textAtBreak = strings.HasSuffix(content, "\n")
	if p.BlockID != "" {
		s.textBlockID = p.BlockID
	}
	return ev
}

func startsWithSpace(s string) bool {
	return s != "" && strings.ContainsRune(" \t\r\n", rune(s[0]))
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
	BlockID  string          `json:"block_id"`
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

// handleSessionNotice forwards go-agent-wrapper v0.17.0's three session
// notices to the typed callback as the go-providers events they came from,
// so the chat bridge can show them (CW-20260930-0113). None is terminal:
// session.lost can mean the turn continued in a new provider session, an
// auth failure is followed by the turn's own turn.failed, and a permission
// refusal lets the turn complete. They never reach the legacy fanout.
func (s *runtimeEventSink) handleSessionNotice(kind runtimeevents.EventKind, raw json.RawMessage) {
	if s.typedCB == nil {
		return
	}
	var p struct {
		RequestedID string `json:"requested_id"`
		ActualID    string `json:"actual_id"`
		Reason      string `json:"reason"`
		Error       string `json:"error"`
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	switch kind {
	case runtimeevents.KindSessionLost:
		s.typedCB(events.SessionLost{RequestedID: p.RequestedID, ActualID: p.ActualID, Reason: p.Reason})
	case runtimeevents.KindSessionAuthFailed:
		s.typedCB(events.AuthFailed{Message: p.Error})
	case runtimeevents.KindAgentPermissionDenied:
		s.typedCB(events.PermissionDenied{Action: p.Action, DisplayName: p.DisplayName})
	default:
		// Write routes only the three notice kinds here.
	}
}

// turnCompletedPayload is the usage-bearing part of a turn's terminal event,
// KindTurnCompleted or KindTurnFailed. Since go-agent-wrapper v0.13.1 a native
// turn has exactly one terminal event, carrying the usage accumulated over the
// turn under "usage" when it reported any. ACP adapters add a top-level
// "stop_reason" beside "usage".
type turnCompletedPayload struct {
	Usage      *llmtypes.Usage `json:"usage"`
	StopReason string          `json:"stop_reason"`
}

// TurnCompletedUsage returns the usage a terminal event's payload
// (KindTurnCompleted or KindTurnFailed) carries, or nil when it carries none.
// For ACP it folds the adapter's top-level stop_reason into Usage.StopReason —
// synthesizing an otherwise-empty Usage if need be — because every consumer
// reads the stop reason from usage and ACP truncation (stop_reason
// "max_tokens") was otherwise never seen (CW-20260930-0113). Since
// go-agent-wrapper v0.17.0 that top-level value is already normalized
// (llmtypes.NormalizeStopReason: ACP's max_turn_requests -> turn_limit); it
// still rides beside usage rather than in it, which is the only thing this
// folds. Native usage carries its own normalized StopReason (go-providers
// v0.35.0), so native payloads are returned unchanged.
func TurnCompletedUsage(raw json.RawMessage, acp bool) *llmtypes.Usage {
	var p turnCompletedPayload
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return nil
	}
	if acp && p.StopReason != "" {
		if p.Usage == nil {
			p.Usage = &llmtypes.Usage{}
		}
		if p.Usage.StopReason == "" {
			p.Usage.StopReason = p.StopReason
		}
	}
	return p.Usage
}

// handleTurnCompleted projects a turn's terminal completion: its usage, when
// it carries any, then Done. Every KindTurnCompleted is terminal — ACP always
// reported usage and completion in one event, and go-agent-wrapper v0.13.1
// does the same for native runtimes, so no second, empty completion follows
// (CW-20261001-0019).
func (s *runtimeEventSink) handleTurnCompleted(ctx context.Context, raw json.RawMessage) {
	if usage := TurnCompletedUsage(raw, s.acp); usage != nil {
		s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: usage})
	}
	s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDone})
}

// turnFailedPayload mirrors translateStreamEvent's {"error": ev.Error}
// shape for llmtypes.EventError. go-agent-wrapper's own failure for a turn the
// child never finished uses the same key, beside reason "process_exited".
type turnFailedPayload struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

// StopReasonInterrupted is the stop reason an interrupted turn's usage
// carries into the chat loop, so the turn is saved as interrupted rather than
// reported as failed.
const StopReasonInterrupted = "interrupted"

// TurnFailedInterrupted reports whether a KindTurnFailed payload is a turn
// ended by CancelTurn (go-agent-wrapper v0.22.0+: reason "interrupted"), not
// a failure.
func TurnFailedInterrupted(raw json.RawMessage) bool {
	var p turnFailedPayload
	return len(raw) > 0 && json.Unmarshal(raw, &p) == nil && p.Reason == "interrupted"
}

// InterruptedTurnUsage is the usage an interrupted turn delivers: its own,
// or an empty one, carrying StopReasonInterrupted.
func InterruptedTurnUsage(raw json.RawMessage, acp bool) *llmtypes.Usage {
	usage := TurnCompletedUsage(raw, acp)
	if usage == nil {
		usage = &llmtypes.Usage{}
	}
	usage.StopReason = StopReasonInterrupted
	return usage
}

// handleTurnFailed projects a failed turn: its usage, when it carries any,
// then the error. Since go-agent-wrapper v0.13.1 a failed turn's usage rides
// on its turn.failed rather than on a completion of its own. A turn ended by
// CancelTurn is not an error: it is usage marked interrupted, then Done
// (CW-20261001-0168).
func (s *runtimeEventSink) handleTurnFailed(ctx context.Context, raw json.RawMessage) {
	if TurnFailedInterrupted(raw) {
		s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: InterruptedTurnUsage(raw, s.acp)})
		s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDone})
		return
	}
	var p turnFailedPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	if usage := TurnCompletedUsage(raw, s.acp); usage != nil {
		s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: usage})
	}
	s.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: UserFacingTurnError(p.Error)})
}
