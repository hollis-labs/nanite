package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	ssekit "github.com/hollis-labs/libs/ui-go/ssekit"
	"github.com/hollis-labs/nanite/internal/store"
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
	if a.Services == nil || a.Services.RuntimeFeed == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "host runtime feed not initialized")
		return
	}
	_, ok := w.(http.Flusher)
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

	stream, err := newSSEWriter(w, true)
	if err != nil {
		return
	}

	cursor := after
	headWritten := false
	var lastHead store.HostRuntimeHead
	writePending := func() bool {
		gapWritten := false
		for {
			replay, replayErr := a.Services.RuntimeFeed.EventsAfter(r.Context(), sessionID, cursor, hostRuntimeReplayPageSize)
			if replayErr != nil {
				return false
			}
			ownerChanged := replay.Head.RuntimeGenerationFloor != lastHead.RuntimeGenerationFloor ||
				replay.Head.CurrentRuntimeRunID != lastHead.CurrentRuntimeRunID
			gapSnapshotChanged := replay.Gap != nil && replay.Head != lastHead
			if !headWritten || ownerChanged || gapSnapshotChanged {
				data, marshalErr := json.Marshal(hostRuntimeHeadToView(replay.Head))
				if marshalErr != nil {
					return false
				}
				// A head is snapshot authority, not a committed feed record. It
				// deliberately has no SSE id, so disconnecting after this frame
				// cannot skip the replay rows that follow it.
				if writeErr := stream.Send(ssekit.Event{Name: "host_runtime.head.v1", Data: data}); writeErr != nil {
					return false
				}
				headWritten = true
				lastHead = replay.Head
			}
			if replay.Gap != nil && !gapWritten {
				data, marshalErr := json.Marshal(hostRuntimeGapToView(replay.Gap))
				if marshalErr != nil {
					return false
				}
				if writeErr := stream.Send(ssekit.Event{ID: strconv.FormatInt(replay.PrunedThrough, 10), Name: "host_runtime.gap.v1", Data: data}); writeErr != nil {
					return false
				}
				gapWritten = true
			}
			for i := range replay.Events {
				event := &replay.Events[i]
				data, marshalErr := json.Marshal(hostRuntimeEventToView(event))
				if marshalErr != nil {
					return false
				}
				if writeErr := stream.Send(ssekit.Event{ID: strconv.FormatInt(event.Cursor, 10), Name: "host_runtime.v1", Data: data}); writeErr != nil {
					return false
				}
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
			if err := stream.Comment("keepalive"); err != nil {
				return
			}
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
