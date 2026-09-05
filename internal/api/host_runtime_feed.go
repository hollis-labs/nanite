package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const hostRuntimeReplayPageSize = 128

// handleHostRuntimeFeed serves the durable, session-scoped runtime feed. The
// SSE id is Nanite's committed per-session cursor; source_sequence remains in
// the versioned DTO and is never interpreted as a replay position.
//
// GET /api/sessions/{id}/runtime-events?after=<cursor>
//
// An explicit query cursor takes precedence over Last-Event-ID. With neither,
// the endpoint replays the bounded retained snapshot from cursor zero so a
// fresh UI can reconstruct current lifecycle and tool state.
func (a *API) handleHostRuntimeFeed(w http.ResponseWriter, r *http.Request) {
	if a.Services == nil || a.Services.Store == nil || a.Services.RuntimeFeed == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "host runtime feed not initialized")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.errorResp(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session id is required")
		return
	}

	after, err := hostRuntimeCursor(r)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	clearSSEWriteDeadline(w)
	flusher.Flush()

	cursor := after
	writePending := func() bool {
		gapWritten := false
		for {
			replay, replayErr := a.Services.Store.HostRuntimeEventsAfter(r.Context(), sessionID, cursor, hostRuntimeReplayPageSize)
			if replayErr != nil {
				return false
			}
			if replay.Gap != nil && !gapWritten {
				data, marshalErr := json.Marshal(replay.Gap)
				if marshalErr != nil {
					return false
				}
				if _, writeErr := fmt.Fprintf(w, "id: %d\nevent: host_runtime.gap.v1\ndata: %s\n\n", replay.PrunedThrough, data); writeErr != nil {
					return false
				}
				flusher.Flush()
				gapWritten = true
			}
			for _, event := range replay.Events {
				data, marshalErr := json.Marshal(event)
				if marshalErr != nil {
					return false
				}
				if _, writeErr := fmt.Fprintf(w, "id: %d\nevent: host_runtime.v1\ndata: %s\n\n", event.Cursor, data); writeErr != nil {
					return false
				}
				flusher.Flush()
			}
			cursor = replay.NextCursor
			if len(replay.Events) < hostRuntimeReplayPageSize || cursor >= replay.LatestCursor {
				return true
			}
		}
	}
	if !writePending() {
		return
	}

	poll := time.NewTicker(100 * time.Millisecond)
	keepalive := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if !writePending() {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func hostRuntimeCursor(r *http.Request) (int64, error) {
	raw := ""
	if values, explicit := r.URL.Query()["after"]; explicit {
		if len(values) > 0 {
			raw = strings.TrimSpace(values[0])
		}
	} else {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		return 0, fmt.Errorf("invalid host runtime cursor %q", raw)
	}
	return cursor, nil
}
