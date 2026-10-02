package api

import (
	"encoding/json"
	"net/http"
	"time"

	ssekit "github.com/hollis-labs/go-ssekit"
	goplugin "github.com/hollis-labs/plugin-sdk"
)

// handleUnifiedEvents provides a single multiplexed SSE connection (/api/events)
// that combines presence events, plugin lifecycle events, and work updates into
// one stream, preventing HTTP/1.1 connection starvation in browsers.
func (a *API) handleUnifiedEvents(w http.ResponseWriter, r *http.Request) {
	_, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Subscribe before the response opens. A client treats the stream as open
	// once headers arrive, so an event emitted after that must already have a
	// subscriber to reach; subscribing after the flush dropped anything
	// broadcast in between (CW-20260930-0143).
	//
	// 1. Presence subscription
	clientID, presenceEvents := a.Services.Streams.RegisterPresenceClient()
	defer a.Services.Streams.UnregisterPresenceClient(clientID)

	// 2. Plugin lifecycle events subscription (if host is configured)
	var pluginCh <-chan goplugin.Event
	if a.pluginHost != nil {
		ch := a.pluginHost.SubscribeEvents()
		defer a.pluginHost.UnsubscribeEvents(ch)
		pluginCh = ch
	}

	stream, err := newSSEWriter(w, true)
	if err != nil {
		return
	}

	// Initial comment establishes connection and flushes headers
	if err := stream.Comment("unified event stream open"); err != nil {
		return
	}

	// Replay current active presence state
	for _, evt := range a.Services.Streams.ActivePresenceState() {
		data, err := json.Marshal(evt)
		if err == nil {
			if err := stream.Send(ssekit.Event{Name: "presence", Data: data}); err != nil {
				return
			}
		}
	}

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case <-keepalive.C:
			if err := stream.Comment("keepalive"); err != nil {
				return
			}

		case pEvt, ok := <-presenceEvents:
			if !ok {
				return
			}
			data, err := json.Marshal(pEvt)
			if err == nil {
				if err := stream.Send(ssekit.Event{Name: "presence", Data: data}); err != nil {
					return
				}
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
				if err := stream.Send(ssekit.Event{Name: "plugin", Data: data}); err != nil {
					return
				}
			}
		}
	}
}
