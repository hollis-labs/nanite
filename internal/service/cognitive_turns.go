package service

import (
	"encoding/json"
	"log/slog"
	"sync"

	agentservice "github.com/hollis-labs/libs/agent/core/service"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// CognitiveTurns binds opaque Nanite output and host event provenance to the
// embeddable native service. Status, snapshot, replay and finalization belong
// to that service; verified definition/model/admission remain host-owned.
type CognitiveTurns struct {
	*agentservice.Turns[store.Message]

	ctxMu    sync.Mutex
	ctxUsed  map[string]int // turn id -> tokens in the context after the run (last call's input + output)
	ctxOrder []string
}

// maxContextUsedEntries bounds the side map; an evicted turn falls back to the
// run's total usage in the stream annotation.
const maxContextUsedEntries = 4096

// SetContextUsed records how many tokens the session context held after the
// run's last model call (its input including cache, plus its output): the
// number a context meter wants, which is smaller than the run total when a tool
// loop made several calls.
func (t *CognitiveTurns) SetContextUsed(turnID string, used int) {
	if t == nil || turnID == "" || used <= 0 {
		return
	}
	t.ctxMu.Lock()
	defer t.ctxMu.Unlock()
	if t.ctxUsed == nil {
		t.ctxUsed = map[string]int{}
	}
	if _, seen := t.ctxUsed[turnID]; !seen {
		t.ctxOrder = append(t.ctxOrder, turnID)
		if len(t.ctxOrder) > maxContextUsedEntries {
			delete(t.ctxUsed, t.ctxOrder[0])
			t.ctxOrder = t.ctxOrder[1:]
		}
	}
	t.ctxUsed[turnID] = used
}

// ContextUsed returns the value SetContextUsed recorded for a turn.
func (t *CognitiveTurns) ContextUsed(turnID string) (int, bool) {
	if t == nil {
		return 0, false
	}
	t.ctxMu.Lock()
	defer t.ctxMu.Unlock()
	v, ok := t.ctxUsed[turnID]
	return v, ok
}

type cognitiveRun struct {
	*agentservice.Run[store.Message]
	owner               *CognitiveTurns
	id                  string
	persistenceReported sync.Once
}

func NewCognitiveTurns(backing ...*store.Store) *CognitiveTurns {
	var port agentservice.SnapshotStore
	if len(backing) > 0 && backing[0] != nil {
		port = backing[0]
	}
	return newCognitiveTurns(port, nil)
}
func newCognitiveTurns(backing agentservice.SnapshotStore, hub *streamhub.Hub) *CognitiveTurns {
	return &CognitiveTurns{Turns: agentservice.NewTurns[store.Message](backing, agentservice.Options{Hub: hub, StateActivityKind: "nanite.turn_state", ActivityPrefix: "nanite."})}
}
func (t *CognitiveTurns) create(viewID, id, provider, model string, mode chat.DeltaMode, effort string, load func() (*store.Message, error)) *cognitiveRun {
	run := t.Create(viewID, id, provider, model, string(mode), effort, func() (agentservice.Committed[store.Message], error) {
		msg, err := load()
		if err != nil {
			return agentservice.Committed[store.Message]{}, err
		}
		committed := agentservice.Committed[store.Message]{Message: msg}
		var metadata struct {
			Interrupted bool `json:"interrupted"`
		}
		_ = json.Unmarshal([]byte(msg.Metadata), &metadata)
		committed.Interrupted = metadata.Interrupted
		var structured chat.StructuredMessage
		if json.Unmarshal([]byte(msg.Content), &structured) == nil && structured.Version > 0 {
			committed.Content = &structured.Text
		}
		return committed, nil
	})
	result := &cognitiveRun{Run: run, owner: t, id: id}
	result.reportPersistence(run.PersistenceError())
	return result
}
func (r *cognitiveRun) consume(event chat.StreamEvent) {
	raw, _ := json.Marshal(event)
	input := agentservice.Input{Type: event.Type, Content: event.Content, Phase: event.Phase, Tool: event.Tool, ToolID: event.ToolID, Detail: event.Detail, Summary: event.Summary, Data: event.Data, Error: event.Error, IsError: event.IsError, Raw: raw}
	if event.StructuredError != nil {
		input.Failure = &chatstream.RunError{Code: string(event.StructuredError.Code), Message: event.StructuredError.Message}
	}
	r.reportPersistence(r.Consume(input))
}
func (r *cognitiveRun) end() { r.reportPersistence(r.End()) }

func (r *cognitiveRun) reportPersistence(err error) {
	if err != nil {
		r.persistenceReported.Do(func() { slog.Error("native turn snapshot durability failure", "turn_id", r.id, "error", err) })
	}
}
