package api

import (
	"encoding/json"
	"net/http"

	ssekit "github.com/hollis-labs/libs/ui-go/ssekit"
)

func (a *API) handlePresenceStream(w http.ResponseWriter, r *http.Request) {
	_, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	stream, err := newSSEWriter(w, false)
	if err != nil {
		return
	}

	// Register this client for presence events.
	clientID, events := a.Services.Streams.RegisterPresenceClient()
	defer a.Services.Streams.UnregisterPresenceClient(clientID)

	// Send current state — all currently-streaming sessions.
	for _, evt := range a.Services.Streams.ActivePresenceState() {
		data, _ := json.Marshal(evt)
		if err := stream.Send(ssekit.Event{Data: data}); err != nil {
			return
		}
	}

	// Stream events until client disconnects.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			data, _ := json.Marshal(evt)
			if err := stream.Send(ssekit.Event{Data: data}); err != nil {
				return
			}
		}
	}
}
