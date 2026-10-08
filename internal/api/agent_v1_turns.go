package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	chatstream "github.com/hollis-labs/go-chatstream"
	"github.com/hollis-labs/go-chatstream/hubbind"
	"github.com/hollis-labs/go-chatstream/sink"
	"github.com/hollis-labs/go-chatstream/sink/native"
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

func (a *API) agentV1Turn(w http.ResponseWriter, r *http.Request) (service.CognitiveTurnSnapshot, bool) {
	view, id := r.PathValue("id"), r.PathValue("turnId")
	if _, err := a.Services.Sessions.Get(r.Context(), view); err != nil {
		a.agentV1LookupError(w, err)
		return service.CognitiveTurnSnapshot{}, false
	}
	if a.Services.Streams == nil {
		a.agentV1Error(w, 503, "native turns unavailable")
		return service.CognitiveTurnSnapshot{}, false
	}
	snapshot, err := a.Services.Streams.CognitiveTurns().Get(view, id)
	if err != nil {
		if errors.Is(err, service.ErrCognitiveTurnNotFound) {
			a.agentV1Error(w, 404, "turn not found in this view")
		} else {
			a.agentV1Error(w, 500, "turn lookup failed")
		}
		return service.CognitiveTurnSnapshot{}, false
	}
	return snapshot, true
}

func (a *API) handleAgentV1GetTurn(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := a.agentV1Turn(w, r)
	if !ok {
		return
	}
	a.jsonResp(w, 200, struct {
		service.CognitiveTurnSnapshot
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
		service.CognitiveTurnSnapshot
		Links agentV1TurnLinks `json:"links"`
	}{snapshot, agentV1TurnRoutes(snapshot.SessionViewID, snapshot.TurnID)})
}

type agentCanonicalEncoder struct{ sink.Encoder }

func (e agentCanonicalEncoder) Headers() http.Header {
	headers := e.Encoder.Headers()
	headers.Set("Cache-Control", "private, no-store, no-transform")
	return headers
}

func (a *API) handleAgentV1TurnEvents(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		a.agentV1Error(w, 400, "event cursors use Last-Event-ID only; query parameters are unsupported")
		return
	}
	var after uint64
	if values := r.Header.Values("Last-Event-ID"); len(values) > 0 {
		if len(values) != 1 || values[0] == "" {
			a.agentV1Error(w, 400, "invalid Last-Event-ID")
			return
		}
		for _, c := range values[0] {
			if c < '0' || c > '9' {
				a.agentV1Error(w, 400, "invalid Last-Event-ID")
				return
			}
		}
		var err error
		after, err = strconv.ParseUint(values[0], 10, 64)
		if err != nil || after == uint64(streamhub.FromLatest) {
			a.agentV1Error(w, 400, "invalid Last-Event-ID")
			return
		}
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
	encoder := agentCanonicalEncoder{native.New()}
	w.Header().Set("X-Chat-Encoding", "chatstream/v1")
	writer, err := sink.Start(w, encoder)
	if err != nil {
		if sub != nil {
			_ = sub.Close()
		}
		return
	}
	if expired {
		gap := chatstream.Event{V: chatstream.SchemaVersion, RunID: snapshot.RunID, Time: time.Now().UTC(), Verb: chatstream.VerbGap, Reason: chatstream.GapRetention, From: after + 1, To: snapshot.EventCheckpoint}
		if after > snapshot.EventCheckpoint {
			gap.Reason = chatstream.GapCursorAhead
			gap.From = after
			gap.To = snapshot.EventCheckpoint
		}
		if after != snapshot.EventCheckpoint {
			_ = encoder.Encode(writer, gap)
		}
		return
	}
	defer func() { _ = sub.Close() }()
	var comments sink.Bind
	for {
		nextCtx, cancel := context.WithTimeout(r.Context(), service.CognitiveKeepalive)
		item, nextErr := sub.Next(nextCtx)
		cancel()
		if errors.Is(nextErr, context.DeadlineExceeded) && r.Context().Err() == nil {
			if comments.Comment(writer, "keepalive") != nil {
				return
			}
			continue
		}
		if nextErr != nil {
			return
		}
		var ev chatstream.Event
		if item.Gap != nil {
			ev = hubbind.GapEvent(snapshot.RunID, time.Now().UTC(), *item.Gap)
		} else {
			ev, err = hubbind.Decode(item.Record)
			if err != nil {
				return
			}
		}
		if encoder.Encode(writer, ev) != nil {
			return
		}
		if ev.IsTerminal() {
			return
		}
	}
	// A downstream disconnect/slow-consumer close must never synthesize a
	// second run outcome. The encoder's Close finalizer is deliberately not
	// used: clients resume the log or load the authoritative status snapshot.
}
