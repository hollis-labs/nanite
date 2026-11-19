package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// Activity kinds the runner publishes.
const (
	// ActivitySlots reports the turn's assembled context. Value: slots, a
	// list of name, tokens and changed.
	ActivitySlots = "nanite.slots"
	// ActivitySubagent reports a child conversation. Value: state ("start"
	// or "done"), id, and role or error.
	ActivitySubagent = "nanite.subagent"
	// ActivityCompact reports history compaction. Value: state ("start",
	// "done" or "failed"), with before, after or error.
	ActivityCompact = "nanite.compact"
)

// CodeTurnFailed is the run.error code of a turn Nanite could not finish.
const CodeTurnFailed = "turn_failed"

// pending is one approval a turn is waiting on.
type pending struct {
	settled chan struct{} // closed once the gate knows whether it must ask
	auto    bool          // a standing grant covered it; nobody is asked
	answer  chan chat.Decision
	closed  bool // answered, or never open to an answer
}

func (p *pending) settle(auto bool) {
	p.auto = auto
	close(p.settled)
}

type run struct {
	runner          *Runner
	turn            chat.Turn
	nanite          string
	provider, model string
	inner           *agent.Run
	ctx             context.Context
	stop            context.CancelFunc

	mu       sync.Mutex
	pending  map[string]*pending
	canceled bool

	// Translation state, used only by the goroutine reading Events.
	asked    map[string]bool // call IDs an approval.request was emitted for
	step     int
	stepOpen bool
	results  bool // the open step has a tool result: the next content is a new step
	part     string
	partKind chatstream.PartKind
	parts    int
}

func (x *run) approval(id string) *pending {
	x.mu.Lock()
	defer x.mu.Unlock()
	p := x.pending[id]
	if p == nil {
		p = &pending{settled: make(chan struct{}), answer: make(chan chat.Decision, 1)}
		x.pending[id] = p
	}
	return p
}

// Cancel asks the turn to stop.
func (x *run) Cancel() {
	x.mu.Lock()
	x.canceled = true
	x.mu.Unlock()
	x.stop()
}

// Answer resolves an approval this run asked for.
func (x *run) Answer(_ context.Context, approvalID string, d chat.Decision) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	p := x.pending[approvalID]
	if p == nil {
		return fmt.Errorf("chatrunner: no approval %q in this run", approvalID)
	}
	select {
	case <-p.settled:
	default:
		return fmt.Errorf("chatrunner: approval %q has not been asked yet", approvalID)
	}
	if p.auto || p.closed {
		return fmt.Errorf("chatrunner: approval %q is already resolved", approvalID)
	}
	p.closed = true
	p.answer <- d
	return nil
}

// Events yields the turn's chatstream events. It is read once.
func (x *run) Events() iter.Seq2[chatstream.Event, error] {
	return func(yield func(chatstream.Event, error) bool) {
		defer x.runner.release(x)
		defer x.stop()
		terminal := false
		for ev := range x.inner.Events() {
			for _, out := range x.translate(ev) {
				terminal = terminal || out.IsTerminal()
				if !yield(out, nil) {
					x.stop()
					_, _ = x.inner.Wait() // let the turn record its end
					return
				}
			}
		}
		if !terminal {
			// Nanite always ends a run with RunFinished or RunFailed.
			for _, out := range x.end(chatstream.Event{Verb: chatstream.VerbRunError, Code: CodeTurnFailed, Message: "the turn ended without a result"}) {
				if !yield(out, nil) {
					return
				}
			}
		}
	}
}

func raw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}

func activity(kind string, value any) chatstream.Event {
	return chatstream.Event{Verb: chatstream.VerbActivity, Kind: kind, Value: raw(value)}
}

func (x *run) stepID() string    { return fmt.Sprintf("step-%d", x.step) }
func (x *run) messageID() string { return fmt.Sprintf("msg-%d", x.step) }

// closePart ends the text or reasoning part being streamed. The Service's
// finalizer fills its final from the deltas.
func (x *run) closePart(out []chatstream.Event) []chatstream.Event {
	if x.part == "" {
		return out
	}
	out = append(out, chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: x.part})
	x.part = ""
	return out
}

func (x *run) closeStep(out []chatstream.Event) []chatstream.Event {
	if !x.stepOpen {
		return out
	}
	out = x.closePart(out)
	out = append(out,
		chatstream.Event{Verb: chatstream.VerbMessageEnd, MessageID: x.messageID()},
		chatstream.Event{Verb: chatstream.VerbStepFinish, StepID: x.stepID()})
	x.stepOpen, x.results = false, false
	return out
}

// content makes sure a step and its assistant message are open for model
// output. Output that follows a tool result belongs to the next model step.
func (x *run) content(out []chatstream.Event) []chatstream.Event {
	if x.stepOpen && x.results {
		out = x.closeStep(out)
	}
	if !x.stepOpen {
		x.step++
		x.stepOpen = true
		out = append(out,
			chatstream.Event{Verb: chatstream.VerbStepStart, StepID: x.stepID()},
			chatstream.Event{Verb: chatstream.VerbMessageStart, MessageID: x.messageID(), Role: "assistant"})
	}
	return out
}

func (x *run) stream(out []chatstream.Event, kind chatstream.PartKind, text string) []chatstream.Event {
	if text == "" {
		return out
	}
	out = x.content(out)
	id := x.part
	if id == "" || x.partKind != kind {
		out = x.closePart(out)
		x.parts++
		id = fmt.Sprintf("%s.%s-%d", x.messageID(), kind, x.parts)
		x.part, x.partKind = id, kind
		out = append(out, chatstream.Event{Verb: chatstream.VerbPartStart, PartID: id, Kind: string(kind)})
	}
	return append(out, chatstream.Event{Verb: chatstream.VerbPartDelta, PartID: id, Text: text})
}

// end closes whatever is open and appends the terminal event.
func (x *run) end(terminal chatstream.Event) []chatstream.Event {
	return append(x.closeStep(nil), terminal)
}

func usage(scope chatstream.UsageScope, u agent.Usage) *chatstream.Usage {
	return &chatstream.Usage{Scope: scope, UncachedInput: u.InputTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens, Output: u.OutputTokens}
}

func finishReason(stop string) chatstream.FinishReason {
	switch stop {
	case "max_steps":
		return chatstream.FinishTurnLimit
	case llmtypes.StopReasonMaxTokens:
		return chatstream.FinishLength
	case "model_context_window_exceeded":
		return chatstream.FinishContextExceeded
	case llmtypes.StopReasonRefusal:
		return chatstream.FinishRefusal
	}
	return chatstream.FinishStop
}

// translate maps one Nanite event to chatstream events.
func (x *run) translate(ev agent.Event) []chatstream.Event {
	var out []chatstream.Event
	switch e := ev.(type) {
	case agent.RunStarted:
		return []chatstream.Event{{Verb: chatstream.VerbRunStart, Provider: x.provider, Model: x.model}}

	case agent.SlotsAssembled:
		type slot struct {
			Name    string `json:"name"`
			Tokens  int    `json:"tokens"`
			Changed bool   `json:"changed"`
		}
		var slots []slot
		for _, name := range agent.SlotNames() {
			if s, ok := e.Report[name]; ok {
				slots = append(slots, slot{string(name), s.Tokens, s.Changed})
			}
		}
		return []chatstream.Event{activity(ActivitySlots, map[string]any{"slots": slots})}

	case agent.TextDelta:
		return x.stream(out, chatstream.PartText, e.Text)

	case agent.Thinking:
		return x.stream(out, chatstream.PartReasoning, e.Text)

	case agent.ToolCallStarted:
		out = x.closePart(x.content(out))
		input := e.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		return append(out,
			chatstream.Event{Verb: chatstream.VerbPartStart, PartID: e.CallID, Kind: string(chatstream.PartToolCall),
				Meta: map[string]json.RawMessage{chatstream.MetaName: raw(e.Name), chatstream.MetaCallID: raw(e.CallID)}},
			chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: e.CallID, Final: input})

	case agent.ToolCallFinished:
		if !x.stepOpen {
			out = x.content(out)
		}
		out = x.closePart(out)
		x.results = true
		id := e.CallID + "#result"
		return append(out,
			chatstream.Event{Verb: chatstream.VerbPartStart, PartID: id, Kind: string(chatstream.PartToolResult),
				Meta: map[string]json.RawMessage{chatstream.MetaCallID: raw(e.CallID), chatstream.MetaIsError: raw(e.IsError)}},
			chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: id, Final: raw(map[string]any{"text": e.Result.Text, "is_error": e.IsError})})

	case agent.PermissionAsked:
		if e.Request.ConversationID != x.nanite {
			return nil // a child conversation's ask; the gate refuses it
		}
		p := x.approval(e.Request.ID)
		select {
		case <-p.settled:
		case <-x.ctx.Done():
			return nil
		}
		if p.auto {
			return nil
		}
		x.asked[e.Request.ID] = true
		t, hosted := x.runner.tool(e.Request.Tool)
		descriptor := map[string]any{
			"description": x.runner.describe(e.Request.Tool, e.Request.Input),
			"standing":    !hosted || t.Level.effective() != LevelConfirm,
			"tool":        e.Request.Tool,
			"input":       e.Request.Input,
		}
		return []chatstream.Event{{Verb: chatstream.VerbApprovalRequest, ApprovalID: e.Request.ID, CallID: e.Request.ID,
			Reason: e.Request.Reason, Descriptor: raw(descriptor), Mode: chatstream.ApprovalInBand}}

	case agent.PermissionResolved:
		if !x.asked[e.ID] {
			return nil // nobody was asked: a rule, a grant or an earlier answer decided
		}
		decision, scope := "deny", chat.ScopeOnce
		if e.Decision.Allow {
			decision = "allow"
			if e.Decision.Scope == agent.ScopeConversation {
				scope = chat.ScopeConversation
			}
		}
		// The Service has already published the outcome of an answer it
		// delivered and drops this one; it is what a viewer sees when the
		// turn ended the wait itself.
		ev := activity(chat.ActivityApproval, map[string]string{"approval_id": e.ID, "decision": decision, "scope": string(scope), "reason": e.Decision.Reason})
		ev.ApprovalID = e.ID
		return []chatstream.Event{ev}

	case agent.Question:
		// Nobody can be asked on this wire yet; decline, as a one-shot run does.
		_ = x.inner.Answer(e.ID, agent.Answer{Value: json.RawMessage(`null`)})
		return nil

	case agent.SubagentStarted:
		return []chatstream.Event{activity(ActivitySubagent, map[string]string{"state": "start", "id": e.ID, "role": e.Role})}

	case agent.SubagentFinished:
		v := map[string]string{"state": "done", "id": e.ID}
		if e.Err != nil {
			v["error"] = e.Err.Error()
		} else if e.Limit != "" {
			v["state"], v["limit"] = "limit", e.Limit
		}
		return []chatstream.Event{activity(ActivitySubagent, v)}

	case agent.CompactionStarted:
		return []chatstream.Event{activity(ActivityCompact, map[string]any{"state": "start", "before": e.Before})}

	case agent.Compacted:
		return []chatstream.Event{activity(ActivityCompact, map[string]any{"state": "done", "before": e.Before, "after": e.After})}

	case agent.CompactionFailed:
		v := map[string]any{"state": "failed"}
		if e.Err != nil {
			v["error"] = e.Err.Error()
		}
		return []chatstream.Event{activity(ActivityCompact, v)}

	case agent.Usage:
		return []chatstream.Event{{Verb: chatstream.VerbUsage, Usage: usage(chatstream.UsageDelta, e)}}

	case agent.RunFinished:
		reason := finishReason(e.Result.StopReason)
		return x.end(chatstream.Event{Verb: chatstream.VerbRunFinish, Reason: string(reason), RawReason: e.Result.StopReason,
			Usage: usage(chatstream.UsageFinal, e.Result.Usage)})

	case agent.RunFailed:
		x.mu.Lock()
		canceled := x.canceled
		x.mu.Unlock()
		if canceled || errors.Is(e.Err, context.Canceled) {
			return x.end(chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: chat.AbortCanceled})
		}
		message := "the turn failed"
		if e.Err != nil {
			message = e.Err.Error()
		}
		return x.end(chatstream.Event{Verb: chatstream.VerbRunError, Code: CodeTurnFailed, Message: message})
	}
	return nil
}
