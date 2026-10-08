package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	chatstream "github.com/hollis-labs/go-chatstream"
	"github.com/hollis-labs/go-chatstream/hubbind"
	streamhub "github.com/hollis-labs/go-streamhub"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

const CognitiveEventRetention = 4096
const CognitiveEventGrace = 300 * time.Second
const CognitiveKeepalive = 15 * time.Second

var ErrCognitiveTurnNotFound = errors.New("turn not found in this view")

// CognitiveTurnSnapshot is captured under the same lock that publishes the
// canonical log. EventCheckpoint and Message therefore describe one revision.
// Turn IDs, run IDs and output IDs currently share one opaque local ID.
type CognitiveTurnSnapshot struct {
	SessionViewID     string              `json:"session_view_id"`
	TurnID            string              `json:"turn_id"`
	RunID             string              `json:"run_id"`
	OutputMessageID   string              `json:"output_message_id"`
	State             string              `json:"state"`
	PendingApprovalID string              `json:"pending_approval_id,omitempty"`
	CancelRequested   bool                `json:"cancel_requested"`
	Revision          uint64              `json:"revision"`
	EventCheckpoint   uint64              `json:"event_checkpoint"`
	DeltaMode         chat.DeltaMode      `json:"delta_mode"`
	Effort            string              `json:"effort"`
	Message           *chatstream.Message `json:"message"`
	Content           string              `json:"content"`
	CommittedMessage  *store.Message      `json:"committed_message,omitempty"`
}

// CognitiveTurns keeps authoritative run reductions independently of the
// bounded event log. Expiring a log does not expire its status/snapshot.
type CognitiveTurns struct {
	mu    sync.Mutex
	runs  map[string]*cognitiveRun
	hub   *streamhub.Hub
	store *store.Store
}

type cognitiveRun struct {
	mu            sync.Mutex
	owner         *CognitiveTurns
	snapshot      CognitiveTurnSnapshot
	canceled      bool
	loadMessage   func() (*store.Message, error)
	partID        string
	phase         string
	partNumber    int
	openedMessage bool
	lastError     *chatstream.RunError
	usage         *chatstream.Usage
}

func NewCognitiveTurns(backing ...*store.Store) *CognitiveTurns {
	t := &CognitiveTurns{runs: make(map[string]*cognitiveRun), hub: streamhub.New(streamhub.NewMemoryLog(),
		streamhub.WithAutoOpen(false), streamhub.WithTerminal(hubbind.Terminal),
		streamhub.WithRetention(streamhub.Retention{MaxRecords: CognitiveEventRetention}),
		streamhub.WithRetainAfterClose(CognitiveEventGrace))}
	if len(backing) > 0 {
		t.store = backing[0]
	}
	return t
}

func (t *CognitiveTurns) create(viewID, id, provider, model string, mode chat.DeltaMode, effort string, load func() (*store.Message, error)) *cognitiveRun {
	r := &cognitiveRun{owner: t, loadMessage: load, snapshot: CognitiveTurnSnapshot{SessionViewID: viewID, TurnID: id, RunID: id, OutputMessageID: id, State: "submitted", DeltaMode: mode, Effort: effort}}
	if err := t.hub.Open(context.Background(), id); err != nil {
		panic(err)
	}
	t.mu.Lock()
	t.runs[id] = r
	t.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publish(chatstream.Event{Verb: chatstream.VerbRunStart, Provider: provider, Model: model})
	r.state("submitted")
	return r
}

func (t *CognitiveTurns) run(viewID, id string) (*cognitiveRun, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[id]
	if r == nil && t.store != nil && viewID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		data, err := t.store.GetCognitiveTurnSnapshot(ctx, viewID, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCognitiveTurnNotFound
		}
		if err != nil {
			return nil, err
		}
		r = &cognitiveRun{owner: t}
		if err = json.Unmarshal([]byte(data), &r.snapshot); err != nil {
			return nil, err
		}
		if r.snapshot.Message == nil {
			r.snapshot.Message = &chatstream.Message{RunID: id, Status: chatstream.StatusStreaming}
		}
		if !r.snapshot.Message.Status.Done() {
			r.snapshot.State = "failed"
			r.snapshot.PendingApprovalID = ""
			r.snapshot.Message.Status = chatstream.StatusErrored
			r.snapshot.Message.Error = &chatstream.RunError{Code: "process_lost", Message: "native process ended before this turn committed an outcome"}
			r.snapshot.Revision++
			if err = r.save(); err != nil {
				return nil, err
			}
		}
		t.runs[id] = r
	}
	if r == nil || (viewID != "" && r.snapshot.SessionViewID != viewID) {
		return nil, ErrCognitiveTurnNotFound
	}
	return r, nil
}

func (t *CognitiveTurns) Get(viewID, id string) (CognitiveTurnSnapshot, error) {
	r, err := t.run(viewID, id)
	if err != nil {
		return CognitiveTurnSnapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := r.snapshot
	snapshot.Message = snapshot.Message.Clone()
	if snapshot.CommittedMessage != nil {
		committed := *snapshot.CommittedMessage
		snapshot.CommittedMessage = &committed
	}
	return snapshot, nil
}

func (t *CognitiveTurns) pendingView(viewID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, run := range t.runs {
		run.mu.Lock()
		pending := run.snapshot.SessionViewID == viewID && !run.snapshot.Message.Status.Done()
		run.mu.Unlock()
		if pending {
			return true
		}
	}
	return false
}

func (t *CognitiveTurns) Subscribe(ctx context.Context, viewID, id string, after uint64) (streamhub.Subscription, error) {
	if _, err := t.run(viewID, id); err != nil {
		return nil, err
	}
	return t.hub.Subscribe(ctx, id, streamhub.SubscribeOptions{After: streamhub.Seq(after), Buffer: 256, Policy: streamhub.CloseAndResume})
}

func (t *CognitiveTurns) CancelRequested(viewID, id string) (CognitiveTurnSnapshot, error) {
	r, err := t.run(viewID, id)
	if err != nil {
		return CognitiveTurnSnapshot{}, err
	}
	r.mu.Lock()
	if !r.snapshot.Message.Status.Done() && !r.snapshot.CancelRequested {
		r.snapshot.CancelRequested = true
		r.snapshot.Revision++
		if err := r.save(); err != nil {
			r.mu.Unlock()
			return CognitiveTurnSnapshot{}, err
		}
	}
	r.mu.Unlock()
	return t.Get(viewID, id)
}

func (t *CognitiveTurns) working(id string) {
	r, err := t.run("", id)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.snapshot.Message.Status.Done() {
		r.state("working")
	}
}

func (t *CognitiveTurns) ending(id string, canceled bool) {
	r, err := t.run("", id)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.canceled = canceled
	r.mu.Unlock()
}

func (r *cognitiveRun) publish(ev chatstream.Event) {
	if r.snapshot.Message != nil && r.snapshot.Message.Status.Done() {
		return
	}
	ev.V, ev.RunID, ev.Time = chatstream.SchemaVersion, r.snapshot.RunID, time.Now().UTC()
	ev.Seq = r.snapshot.EventCheckpoint + 1
	reduced, err := chatstream.Reduce([]chatstream.Event{ev}, r.snapshot.Message)
	if err != nil {
		r.lastError = &chatstream.RunError{Code: "canonical_event_invalid", Message: "native producer emitted an invalid canonical sequence"}
		return
	}
	prior := r.snapshot.Message
	r.snapshot.Message = reduced
	r.snapshot.EventCheckpoint = ev.Seq
	r.snapshot.Revision++
	if saveErr := r.save(); saveErr != nil {
		r.snapshot.State = "failed"
		ev = chatstream.Event{V: ev.V, RunID: ev.RunID, Time: ev.Time, Seq: ev.Seq, Verb: chatstream.VerbRunError, Code: "persistence_failed", Message: "failed to commit native turn snapshot"}
		reduced, _ = chatstream.Reduce([]chatstream.Event{ev}, prior)
		r.snapshot.Message = reduced
		_ = r.save()
	}
	// Prepare the authoritative reduction before fan-out. A status reader
	// waits on this lock until the hub has assigned the same checkpoint.
	published, err := hubbind.Publish(context.Background(), r.owner.hub, r.snapshot.RunID, ev)
	if err != nil {
		slog.Error("cognitive turn publish failed", "turn_id", r.snapshot.TurnID, "error", err)
		return
	}
	reduced.LastSeq = published.Seq
	r.snapshot.Message = reduced
	r.snapshot.EventCheckpoint = published.Seq
}

func jsonValue(v any) json.RawMessage { data, _ := json.Marshal(v); return data }

func (r *cognitiveRun) state(state string) {
	if r.snapshot.Message != nil && r.snapshot.Message.Status.Done() {
		return
	}
	r.snapshot.State = state
	r.publish(chatstream.Event{Verb: chatstream.VerbActivity, Kind: "nanite.turn_state", Value: jsonValue(map[string]string{"state": state})})
}

func (r *cognitiveRun) message() {
	if !r.openedMessage {
		r.publish(chatstream.Event{Verb: chatstream.VerbMessageStart, MessageID: r.snapshot.OutputMessageID, Role: "assistant"})
		r.openedMessage = true
	}
}

func (r *cognitiveRun) closePart() {
	if r.partID != "" {
		r.publish(chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: r.partID})
		r.partID = ""
	}
}

func (r *cognitiveRun) consume(evt chat.StreamEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snapshot.Message.Status.Done() {
		return
	}
	switch evt.Type {
	case "stream_start":
		r.message()
	case "delta":
		r.message()
		if r.partID == "" || r.phase != evt.Phase {
			r.closePart()
			r.partNumber++
			r.partID = fmt.Sprintf("text-%d", r.partNumber)
			r.phase = evt.Phase
			kind := chatstream.PartText
			if evt.Phase == chat.PhaseThinking {
				kind = chatstream.PartReasoning
			}
			r.publish(chatstream.Event{Verb: chatstream.VerbPartStart, PartID: r.partID, Kind: string(kind), Meta: map[string]json.RawMessage{chatstream.MetaPhase: jsonValue(evt.Phase)}})
		}
		if evt.Phase == chat.PhaseFinal || evt.Phase == "" {
			r.snapshot.Content += evt.Content
		}
		r.publish(chatstream.Event{Verb: chatstream.VerbPartDelta, PartID: r.partID, Text: evt.Content})
	case "replace_content":
		r.closePart()
		r.snapshot.Content = evt.Content
		r.publish(chatstream.Event{Verb: chatstream.VerbActivity, Kind: chatstream.ActivityReplaceContent, Value: jsonValue(map[string]string{"content": evt.Content})})
	case "tool_call", "tool_result":
		r.message()
		r.closePart()
		r.snapshot.PendingApprovalID = ""
		r.state("working")
		r.partNumber++
		id := fmt.Sprintf("%s-%d", evt.Type, r.partNumber)
		if evt.Type == "tool_call" {
			id = evt.ToolID
		}
		kind := chatstream.PartToolCall
		if evt.Type == "tool_result" {
			kind = chatstream.PartToolResult
		}
		meta := map[string]json.RawMessage{chatstream.MetaName: jsonValue(evt.Tool), chatstream.MetaCallID: jsonValue(evt.ToolID), chatstream.MetaDetail: jsonValue(evt.Detail), chatstream.MetaIsError: jsonValue(evt.IsError)}
		r.publish(chatstream.Event{Verb: chatstream.VerbPartStart, PartID: id, Kind: string(kind), Meta: meta})
		var final json.RawMessage
		if evt.Type == "tool_result" {
			final = jsonValue(map[string]string{"summary": evt.Summary})
		}
		r.publish(chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: id, Final: final})
	case "approval_request":
		var prompt chat.ApprovalRequestPayload
		if err := json.Unmarshal([]byte(evt.Data), &prompt); err != nil {
			r.lastError = &chatstream.RunError{Code: "invalid_approval", Message: "invalid native permission prompt"}
			return
		}
		r.snapshot.PendingApprovalID = prompt.RequestID
		r.state("input_required")
		expires := prompt.ExpiresAt
		r.publish(chatstream.Event{Verb: chatstream.VerbApprovalRequest, ApprovalID: prompt.RequestID, CallID: prompt.CallID, Reason: prompt.Reason, Descriptor: jsonValue(map[string]any{"tool": prompt.Tool, "input": prompt.Input, "supported_scopes": []string{"once"}}), Mode: chatstream.ApprovalInBand, ExpiresAt: &expires})
	case "error":
		r.lastError = &chatstream.RunError{Code: "provider_error", Message: evt.Error}
		if evt.StructuredError != nil {
			r.lastError.Code = string(evt.StructuredError.Code)
			r.lastError.Message = evt.StructuredError.Message
		}
	case "stream_end":
		r.finish(true)
	case "status", "tool_warning", "notify_pause", "plugin_envelope", "panel_signal", "subagent_run_status_changed", "message_received", "circuit_open", "rate_budget_pause", "handoff_loaded", "slot_changed":
		r.publish(chatstream.Event{Verb: chatstream.VerbActivity, Kind: "nanite." + evt.Type, Value: jsonValue(evt)})
	}
}

func (r *cognitiveRun) end() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.snapshot.Message.Status.Done() {
		r.finish(false)
	}
}

// Only this finalizer publishes terminal events, after the producer has
// committed its final or partial assistant row. Queued cancellation has no
// assistant row because execution never began.
func (r *cognitiveRun) finish(completed bool) {
	r.closePart()
	if r.openedMessage {
		r.publish(chatstream.Event{Verb: chatstream.VerbMessageEnd, MessageID: r.snapshot.OutputMessageID})
	}
	terminal := chatstream.Event{Verb: chatstream.VerbRunError, Code: chatstream.CodeUpstreamTruncated, Message: "native turn ended without a terminal outcome"}
	state := "failed"
	if r.lastError != nil {
		terminal.Code, terminal.Message, terminal.Retryable = r.lastError.Code, r.lastError.Message, r.lastError.Retryable
	}
	canceled := r.canceled
	if canceled {
		terminal = chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: "canceled"}
		state = "canceled"
	}
	msg, err := r.loadMessage()
	if err == nil {
		r.snapshot.CommittedMessage = msg
		var metadata struct {
			Interrupted bool `json:"interrupted"`
		}
		_ = json.Unmarshal([]byte(msg.Metadata), &metadata)
		if completed {
			canceled = metadata.Interrupted
		}
		if canceled {
			terminal = chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: "canceled"}
			state = "canceled"
		}
		var structured chat.StructuredMessage
		if json.Unmarshal([]byte(msg.Content), &structured) == nil && structured.Version > 0 {
			r.snapshot.Content = structured.Text
		}
		if completed && !canceled && r.lastError == nil {
			terminal = chatstream.Event{Verb: chatstream.VerbRunFinish, Reason: "stop"}
			state = "completed"
		}
	} else if completed || (canceled && r.openedMessage) {
		terminal = chatstream.Event{Verb: chatstream.VerbRunError, Code: "persistence_failed", Message: "failed to commit assistant response"}
		state = "failed"
	}

	terminal.Usage = r.usage
	r.snapshot.PendingApprovalID = ""
	r.snapshot.State = state
	r.publish(terminal)
}

func (r *cognitiveRun) save() error {
	if r.owner.store == nil {
		return nil
	}
	data, err := json.Marshal(r.snapshot)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return r.owner.store.SaveCognitiveTurnSnapshot(ctx, r.snapshot.SessionViewID, r.snapshot.TurnID, string(data))
}

func (t *CognitiveTurns) setUsage(id string, usage *chatstream.Usage) {
	r, err := t.run("", id)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.usage = usage
	r.mu.Unlock()
}
