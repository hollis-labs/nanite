package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	goplugin "github.com/hollis-labs/plugin-sdk"
)

// handleUnifiedEvents provides a single multiplexed SSE connection (/api/events)
// that combines presence events, plugin lifecycle events, and work updates into
// one stream, preventing HTTP/1.1 connection starvation in browsers.
func (a *API) handleUnifiedEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	clearSSEWriteDeadline(w)

	// Initial comment establishes connection and flushes headers
	fmt.Fprint(w, ": unified event stream open\n\n")
	flusher.Flush()

	// 1. Presence subscription
	clientID, presenceEvents := a.Services.Streams.RegisterPresenceClient()
	defer a.Services.Streams.UnregisterPresenceClient(clientID)

	// Replay current active presence state
	for _, evt := range a.Services.Streams.ActivePresenceState() {
		data, err := json.Marshal(evt)
		if err == nil {
			fmt.Fprintf(w, "event: presence\ndata: %s\n\n", data)
		}
	}
	flusher.Flush()

	// 2. Plugin lifecycle events subscription (if host is configured)
	var pluginCh <-chan goplugin.Event
	if a.pluginHost != nil {
		ch := a.pluginHost.SubscribeEvents()
		defer a.pluginHost.UnsubscribeEvents(ch)
		pluginCh = ch
	}

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()

		case pEvt, ok := <-presenceEvents:
			if !ok {
				return
			}
			data, err := json.Marshal(pEvt)
			if err == nil {
				fmt.Fprintf(w, "event: presence\ndata: %s\n\n", data)
				flusher.Flush()
			}

		case plEvt, ok := <-pluginCh:
			if !ok {
				pluginCh = nil
				continue
			}
			if !lifecycleEventTypes[plEvt.Type] {
				continue
			}
			data, err := json.Marshal(plEvt)
			if err == nil {
				fmt.Fprintf(w, "event: plugin\ndata: %s\n\n", data)
				flusher.Flush()
			}
		}
	}
}
