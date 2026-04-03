package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// handleEventStream serves an SSE stream of plugin events. Clients can filter
// by event type using the optional ?events= query parameter (comma-separated).
//
// Example: GET /api/plugins/events/stream?events=tool.called,session.start
func (a *API) handleEventStream(w http.ResponseWriter, r *http.Request) {
	if a.Services.Plugins == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "plugin system not initialized")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Parse optional event type filter.
	var filterTypes map[string]bool
	if eventsParam := r.URL.Query().Get("events"); eventsParam != "" {
		filterTypes = make(map[string]bool)
		for _, t := range strings.Split(eventsParam, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				filterTypes[t] = true
			}
		}
	}

	// Subscribe to the event bus.
	ch := a.Services.Plugins.SubscribeEvents()
	defer a.Services.Plugins.UnsubscribeEvents(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}

			// Apply event type filter if specified.
			if filterTypes != nil && !filterTypes[event.Type] {
				continue
			}

			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		}
	}
}
