package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"

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
	// ActivitySubagent reports a subtask's child conversation. Value: state
	// ("start", "progress", "done" or "limit"), id, parent_id, depth (1 for a
	// root's child, 2 under a lead) and variant; when done, status (ok,
	// partial, limit, error, cancelled or denied) and error or limit.
	// Progress carries step, tool and status_line, rate-limited per subtask.
	// A background subtask's start and progress carry background: true, and
	// its last progress carries its status. State "usage" is a finished
	// background subtask's token use, which is not part of the turn's usage
	// deltas.
	ActivitySubagent = "nanite.subagent"
	// ActivityCompact reports history compaction. Value: state ("start",
	// "done" or "failed"), with before, after or error.
	ActivityCompact = "nanite.compact"
	// ActivityNotice reports a notice posted to the conversation: a
	// background subtask or job finishing, a relayed ask waiting for a turn,
	// or the host's own. Value: id, kind and body. It arrives on the running
	// turn, or, when none runs, as a run of its own (Options.Observe).
	ActivityNotice = "nanite.notice"
	// ActivitySteer reports what steering decided before the model ran.
	// Value: mode, mode_source ("deliberate", "classified", "default"),
	// slots, why, fired, halted, and suggestions the person may accept
	// (each: facet, label, p). Reflex actions for the host are not sent.
	ActivitySteer = "nanite.steer"
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
	// ended is closed once Events has finished (nil for a run nobody
	// waits on).
	ended chan struct{}
	// notice is the notice that started a notice turn.
	notice *agent.NoticePosted
	// prelude is activity the run opens with (the front desk's plan).
	prelude []chatstream.Event
	// hostDispatch marks the front desk's own dispatch, which is never
	// published and does not count as the owner's turn ending.
	hostDispatch bool
	// finishing is set once the run has yielded its terminal event: the
	// owner may already send the next message while it releases.
	finishing atomic.Bool

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
		if x.ended != nil {
			defer close(x.ended)
		}
		defer x.runner.release(x)
		defer x.stop()
		terminal := false
		for ev := range x.inner.Events() {
			if s, ok := ev.(agent.Steered); ok && x.runner.opts.OnSteered != nil {
				x.runner.opts.OnSteered(x.turn.ConversationID, s)
			}
			for _, out := range x.translate(ev) {
				if out.IsTerminal() {
					terminal = true
					x.finishing.Store(true)
				}
				if !yield(out, nil) {
					x.stop()
					_, _ = x.inner.Wait() // let the turn record its end
					return
				}
			}
		}
		if !terminal {
			x.finishing.Store(true)
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

func noticeActivity(n agent.NoticePosted) chatstream.Event {
	return activity(ActivityNotice, map[string]any{"id": n.ID, "kind": n.Kind, "body": n.Body})
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
		out = append(out, chatstream.Event{Verb: chatstream.VerbRunStart, Provider: x.provider, Model: x.model})
		if x.notice != nil {
			// A notice turn says first which notice started it.
			out = append(out, noticeActivity(*x.notice))
		}
		return append(out, x.prelude...)

	case agent.SlotsAssembled:
		type slot struct {
			Name    string `json:"name"`
			Tokens  int    `json:"tokens"`
			Changed bool   `json:"changed"`
		}
		type cut struct {
			Slot        string `json:"slot"`
			Owner       string `json:"owner"`
			Key         string `json:"key"`
			Disposition string `json:"disposition"`
			Reason      string `json:"reason,omitempty"`
		}
		var slots []slot
		var cuts []cut
		for _, name := range agent.SlotNames() {
			s, ok := e.Report[name]
			if !ok {
				continue
			}
			slots = append(slots, slot{string(name), s.Tokens, s.Changed})
			for _, d := range s.Decisions {
				switch d.Disposition {
				case agent.SlotTrim:
					cuts = append(cuts, cut{string(name), d.Owner, d.Key, "trim", d.Reason})
				case agent.SlotSkip:
					cuts = append(cuts, cut{string(name), d.Owner, d.Key, "skip", d.Reason})
				case agent.SlotPointer:
					cuts = append(cuts, cut{string(name), d.Owner, d.Key, "pointer", d.Reason})
				case agent.SlotShip:
				}
			}
		}
		value := map[string]any{"slots": slots}
		if len(cuts) > 0 {
			// Entries the host wrote that did not ship whole, so it can see them.
			value["cuts"] = cuts
		}
		return []chatstream.Event{activity(ActivitySlots, value)}

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
			return nil // not this conversation's: a subtask's ask arrives relayed, under the root's ID
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
		callID := e.Request.ID
		if o := e.Request.Subtask; o != nil {
			// A subtask's ask, relayed to this conversation: say which one.
			// It is answered once: the chat Service would keep a standing
			// grant against the root chat, which would reach the root and
			// the subtask's siblings.
			who := "subtask"
			if o.Dispatched {
				who = "subtask (started by the host)"
			}
			descriptor["description"] = fmt.Sprintf("%s %q wants %s: %s (this call only)", who, o.Goal, e.Request.Tool, descriptor["description"])
			descriptor["subtask"] = o
			descriptor["standing"] = false
			descriptor["grant_scope"] = "once"
			if o.CallID != "" {
				callID = o.CallID // the root's subtask call, so a client can attach the card to it
			}
		}
		return []chatstream.Event{{Verb: chatstream.VerbApprovalRequest, ApprovalID: e.Request.ID, CallID: callID,
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
		v := map[string]any{"state": "start", "id": e.ID, "parent_id": e.ParentID, "depth": e.Depth, "variant": e.Variant}
		if e.Background {
			v["background"] = true
		}
		return []chatstream.Event{activity(ActivitySubagent, v)}

	case agent.SubtaskProgress:
		v := map[string]any{"state": "progress", "id": e.ID, "parent_id": e.ParentID, "depth": e.Depth, "variant": e.Variant,
			"step": e.Step, "tool": e.Tool, "status_line": e.StatusLine}
		if e.Background {
			v["background"] = true
		}
		if e.Status != "" {
			v["status"] = e.Status
		}
		return []chatstream.Event{activity(ActivitySubagent, v)}

	case agent.SubagentFinished:
		v := map[string]any{"state": "done", "id": e.ID, "parent_id": e.ParentID, "depth": e.Depth, "variant": e.Variant, "status": e.Status}
		if e.Err != nil {
			v["error"] = e.Err.Error()
		} else if e.Limit != "" {
			v["state"], v["limit"] = "limit", e.Limit
		}
		return []chatstream.Event{activity(ActivitySubagent, v)}

	case agent.NoticePosted:
		return []chatstream.Event{noticeActivity(e)}

	case agent.Steered:
		if e.Event != "" {
			return []chatstream.Event{activity(ActivitySteer, map[string]any{"event": e.Event, "fired": e.Fired})}
		}
		suggestions := make([]map[string]any, 0, len(e.Suggestions))
		for _, v := range e.Suggestions {
			suggestions = append(suggestions, map[string]any{"facet": string(v.Facet), "label": v.Label, "p": v.P})
		}
		return []chatstream.Event{activity(ActivitySteer, map[string]any{"mode": e.Mode, "mode_source": e.ModeSource, "slots": e.Slots,
			"why": e.Why, "fired": e.Fired, "halted": e.Halted, "suggestions": suggestions})}

	case agent.CompactionStarted:
		return []chatstream.Event{activity(ActivityCompact, map[string]any{"state": "start", "before": e.Before, "request_before": e.RequestBefore})}

	case agent.Compacted:
		return []chatstream.Event{activity(ActivityCompact, map[string]any{"state": "done", "before": e.Before, "after": e.After,
			"request_before": e.RequestBefore, "request_after": e.RequestAfter})}

	case agent.CompactionFailed:
		v := map[string]any{"state": "failed"}
		if e.Err != nil {
			v["error"] = e.Err.Error()
		}
		return []chatstream.Event{activity(ActivityCompact, v)}

	case agent.Usage:
		if e.Background {
			// Not this turn's spend, so not a delta toward its final usage.
			return []chatstream.Event{activity(ActivitySubagent, map[string]any{"state": "usage", "id": e.Subtask,
				"uncached_input": e.InputTokens, "cache_read": e.CacheReadTokens, "cache_write": e.CacheWriteTokens, "output": e.OutputTokens})}
		}
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
		if w, ok := agent.IsAuthWaiting(e.Err); ok {
			message = x.runner.authWaitingText(x.nanite, w)
		} else if e.Err != nil {
			message = e.Err.Error()
		}
		return x.end(chatstream.Event{Verb: chatstream.VerbRunError, Code: CodeTurnFailed, Message: message})
	}
	return nil
}
