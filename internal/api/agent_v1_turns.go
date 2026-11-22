package api

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/hollis-labs/nanite/internal/store"
	agentservice "github.com/hollis-labs/substrate/agent/service"
	"github.com/hollis-labs/substrate/agent/transport/httpstream"

	streamhub "github.com/hollis-labs/go-streamhub"
	"github.com/hollis-labs/nanite/internal/service"
)

type agentV1TurnLinks struct {
	Status   string `json:"status"`
	Snapshot string `json:"snapshot"`
	Events   string `json:"events"`
	Cancel   string `json:"cancel"`
}

func agentV1TurnRoutes(view, id string) agentV1TurnLinks {
	status := agentV1RoutePrefix + "/sessions/" + url.PathEscape(view) + "/turns/" + url.PathEscape(id)
	return agentV1TurnLinks{Status: status, Snapshot: status, Events: status + "/events", Cancel: status + "/cancel"}
}

func (a *API) agentV1Turn(w http.ResponseWriter, r *http.Request) (agentservice.Snapshot[store.Message], bool) {
	view, id := r.PathValue("id"), r.PathValue("turnId")
	if _, err := a.Services.Sessions.Get(r.Context(), view); err != nil {
		a.agentV1LookupError(w, err)
		return agentservice.Snapshot[store.Message]{}, false
	}
	if a.Services.Streams == nil {
		a.agentV1Error(w, 503, "native turns unavailable")
		return agentservice.Snapshot[store.Message]{}, false
	}
	snapshot, err := a.Services.Streams.CognitiveTurns().Get(view, id)
	if err != nil {
		if errors.Is(err, agentservice.ErrTurnNotFound) {
			a.agentV1Error(w, 404, "turn not found in this view")
		} else {
			a.agentV1Error(w, 500, "turn lookup failed")
		}
		return agentservice.Snapshot[store.Message]{}, false
	}
	return snapshot, true
}

func (a *API) handleAgentV1GetTurn(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := a.agentV1Turn(w, r)
	if !ok {
		return
	}
	a.jsonResp(w, 200, struct {
		agentservice.Snapshot[store.Message]
		Links agentV1TurnLinks `json:"links"`
	}{snapshot, agentV1TurnRoutes(snapshot.SessionViewID, snapshot.TurnID)})
}

func (a *API) handleAgentV1CancelTurn(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := a.agentV1Turn(w, r)
	if !ok {
		return
	}
	canceler, ok := a.Services.Chat.(service.CognitiveTurnCancellation)
	if !ok {
		a.agentV1Error(w, 503, "targeted cancellation unavailable")
		return
	}
	// Mark intent before signaling the exact generation. A completion that
	// already committed wins; cancellation never rewrites a terminal state.
	if _, err := a.Services.Streams.CognitiveTurns().CancelRequested(snapshot.SessionViewID, snapshot.TurnID); err != nil {
		a.agentV1Error(w, 500, "failed to record cancellation")
		return
	}
	if !snapshot.Message.Status.Done() {
		canceler.CancelCognitiveTurn(snapshot.SessionViewID, snapshot.TurnID)
	}
	snapshot, _ = a.Services.Streams.CognitiveTurns().Get(snapshot.SessionViewID, snapshot.TurnID)
	a.jsonResp(w, 200, struct {
		agentservice.Snapshot[store.Message]
		Links agentV1TurnLinks `json:"links"`
	}{snapshot, agentV1TurnRoutes(snapshot.SessionViewID, snapshot.TurnID)})
}

func (a *API) handleAgentV1TurnEvents(w http.ResponseWriter, r *http.Request) {
	after, err := httpstream.ParseCursor(r)
	if err != nil {
		a.agentV1Error(w, 400, err.Error())
		return
	}
	snapshot, ok := a.agentV1Turn(w, r)
	if !ok {
		return
	}
	sub, err := a.Services.Streams.CognitiveTurns().Subscribe(r.Context(), snapshot.SessionViewID, snapshot.TurnID, after)
	expired := errors.Is(err, streamhub.ErrUnknownStream)
	if err != nil && !expired {
		a.agentV1Error(w, 503, "native event log unavailable")
		return
	}
	_ = httpstream.Write(w, r, sub, httpstream.State{RunID: snapshot.RunID, After: after, Checkpoint: snapshot.EventCheckpoint}, expired)
}
