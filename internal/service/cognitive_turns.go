package service

import (
	"encoding/json"
	"log/slog"
	"sync"

	chatstream "github.com/hollis-labs/libs/ui-go/chatstream"
	streamhub "github.com/hollis-labs/libs/ui-go/streamhub"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	agentservice "github.com/hollis-labs/substrate/agent/service"
)

// CognitiveTurns binds opaque Nanite output and host event provenance to the
// embeddable native service. Status, snapshot, replay and finalization belong
// to that service; verified definition/model/admission remain host-owned.
type CognitiveTurns struct {
	*agentservice.Turns[store.Message]
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
	return &CognitiveTurns{agentservice.NewTurns[store.Message](backing, agentservice.Options{Hub: hub, StateActivityKind: "nanite.turn_state", ActivityPrefix: "nanite."})}
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
