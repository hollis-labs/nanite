package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

// lifecycleEventTypes is the fixed set of event types streamed by
// GET /api/plugins/events. Any event the host broadcasts that is NOT in this
// set is filtered out at the handler — this endpoint is dedicated to plugin
// lifecycle consumers (cache invalidation, toast notifications, etc.) and
// deliberately does not leak the full event bus.
//
// Wire shape per plan §B.8:
//   - plugin.installed   — { plugin_id, version }
//   - plugin.uninstalled — { plugin_id }
//   - plugin.updated     — { plugin_id, from_version, to_version } (TODO: no trigger yet)
//   - plugin.enabled     — { plugin_id }
//   - plugin.disabled    — { plugin_id }
//   - plugin.load_failed — { plugin_id, reason }
var lifecycleEventTypes = map[string]bool{
	naniteplugin.EventPluginInstalled:       true,
	naniteplugin.EventPluginUninstalled:     true,
	naniteplugin.EventPluginUpdated:         true,
	naniteplugin.EventPluginEnabled:         true,
	naniteplugin.EventPluginDisabled:        true,
	naniteplugin.EventPluginLoadFailed:      true,
	naniteplugin.EventPluginInstallProgress: true,
}

// sseKeepaliveInterval is the cadence at which we send an SSE comment to keep
// intermediate proxies from closing an idle stream. 25s is below the typical
// 30s idle-timeout boundary.
const sseKeepaliveInterval = 25 * time.Second

// registerPluginsEventsRoute wires GET /api/plugins/events onto mux.
// Replaces the old dead GET /api/plugins/events/stream (deleted in B.8).
func registerPluginsEventsRoute(mux *http.ServeMux, host *naniteplugin.Host) {
	mux.HandleFunc("GET /api/plugins/events", func(w http.ResponseWriter, r *http.Request) {
		handlePluginsEvents(w, r, host)
	})
}

func handlePluginsEvents(w http.ResponseWriter, r *http.Request, host *naniteplugin.Host) {
	if host == nil {
		http.Error(w, "plugin system not initialized", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := host.SubscribeEvents()
	defer host.UnsubscribeEvents(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// Initial comment flushes headers and establishes the stream before the
	// first event arrives.
	fmt.Fprint(w, ": plugin lifecycle stream open\n\n")
	flusher.Flush()

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
		case event, open := <-ch:
			if !open {
				return
			}
			if !lifecycleEventTypes[event.Type] {
				continue
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, payload)
			flusher.Flush()
		}
	}
}
