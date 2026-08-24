package chat

import (
	"context"
	"sync"

	"github.com/hollis-labs/nanite/internal/store"
)

// ResponseHandler runs server-side side effects and builds the transcript
// payload when a typed envelope response arrives via POST /api/envelopes/:id/respond.
// See plans/phase-3-s5-envelope-typed-responses.md §T3.
type ResponseHandler interface {
	HandleResponse(ctx context.Context, env store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error)
}

// HandlerResult is what the response endpoint threads into the transcript.
//
// TranscriptData is merged into the envelope_response message payload. Silent
// suppresses the transcript write entirely (pure side-effect handlers).
// FollowUp is an optional short string appended as a system note so the agent
// sees a human-readable summary of the side effect.
type HandlerResult struct {
	TranscriptData map[string]any
	Silent         bool
	FollowUp       string
}

// HandlerFunc is an adapter so ordinary functions can satisfy ResponseHandler.
type HandlerFunc func(ctx context.Context, env store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error)

func (f HandlerFunc) HandleResponse(ctx context.Context, env store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error) {
	return f(ctx, env, resp)
}

var (
	responseHandlersMu sync.RWMutex
	responseHandlers   = map[string]ResponseHandler{}
)

// RegisterResponseHandler binds a ResponseHandler to an envelope type. Later
// registrations overwrite earlier ones — plugins replace the default on load
// and restore it on unload via UnregisterResponseHandler.
func RegisterResponseHandler(envelopeType string, h ResponseHandler) {
	responseHandlersMu.Lock()
	responseHandlers[envelopeType] = h
	responseHandlersMu.Unlock()
}

// UnregisterResponseHandler removes a handler binding. Safe to call for a type
// with no registration (no-op). Used by the plugin host during UnloadPlugin.
func UnregisterResponseHandler(envelopeType string) {
	responseHandlersMu.Lock()
	delete(responseHandlers, envelopeType)
	responseHandlersMu.Unlock()
}

// LookupResponseHandler returns the registered handler for envelopeType, or
// the default pass-through if none is registered.
func LookupResponseHandler(envelopeType string) ResponseHandler {
	responseHandlersMu.RLock()
	h, ok := responseHandlers[envelopeType]
	responseHandlersMu.RUnlock()
	if ok {
		return h
	}
	return defaultResponseHandler{}
}

// defaultResponseHandler is the fallback for envelope types that haven't
// registered one. It surfaces the response Data verbatim into the transcript
// and performs no side effects.
type defaultResponseHandler struct{}

func (defaultResponseHandler) HandleResponse(_ context.Context, _ store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error) {
	// Copy resp.Data into a fresh map rather than mutating the caller's map
	// in place. The endpoint reuses resp for response_json marshaling, and
	// mutation would leak "answers"/"decisions" into the persisted payload.
	data := make(map[string]any, len(resp.Data)+2)
	for k, v := range resp.Data {
		data[k] = v
	}
	if len(resp.Answers) > 0 {
		data["answers"] = resp.Answers
	}
	if len(resp.Decisions) > 0 {
		data["decisions"] = resp.Decisions
	}
	return HandlerResult{TranscriptData: data}, nil
}
