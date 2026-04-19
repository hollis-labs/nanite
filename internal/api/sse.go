package api

import (
	"log/slog"
	"net/http"
	"time"
)

// clearSSEWriteDeadline disables the server-global WriteTimeout for a
// long-lived SSE stream. SSE handlers call this right after the initial
// WriteHeader so the per-connection write deadline is cleared; the global
// 60s WriteTimeout (defined in internal/server/server.go) still protects
// every non-SSE endpoint from Slowloris / slow-body DoS.
//
// Without this call, SSE streams are silently cut off at 60s — see UAT c18
// (CW-20260419-0020): the chat generation loop kept running past 60s but
// the HTTP layer closed the response, so clients saw "stall", the accumulated
// text never reached the UI, and only a page refresh (reading from the
// persisted assistant message) surfaced the result.
//
// http.NewResponseController was added in Go 1.20.
func clearSSEWriteDeadline(w http.ResponseWriter) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("sse: could not clear write deadline", "err", err)
	}
}
