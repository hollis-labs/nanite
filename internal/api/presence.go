package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (a *API) handlePresenceStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Register this client for presence events.
	clientID, events := a.Services.Streams.RegisterPresenceClient()
	defer a.Services.Streams.UnregisterPresenceClient(clientID)

	// Send current state — all currently-streaming sessions.
	for _, evt := range a.Services.Streams.ActivePresenceState() {
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

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
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
