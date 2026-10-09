package api

import (
	"net/http"

	ssekit "github.com/hollis-labs/libs/ui-go/ssekit"
)

// newSSEWriter keeps Nanite's response headers while delegating framing,
// deadline clearing and checked write/flush operations to ssekit. The caller
// validates before opening the response. UAT c18 / CW-20260419-0020 found
// SSE streams silently cut off by the server write timeout; clearing deadlines
// here lets long-running approvals and tool calls keep their connection.
// A failed initial flush has already committed HTTP 200 and must simply end
// the handler.
func newSSEWriter(w http.ResponseWriter, bufferingHint bool) (*ssekit.Writer, error) {
	options := []ssekit.WriterOption{
		ssekit.WithCacheControl("no-cache"),
		ssekit.WithHeader("Connection", "keep-alive"),
	}
	if !bufferingHint {
		options = append(options, ssekit.WithoutBufferingHint())
	}
	return ssekit.NewWriter(w, options...)
}
