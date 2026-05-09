package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
)

// fakeExecutor is a controllable Executor for the route-dispatch wiring
// tests. Captures the request it received and returns the canned
// response. Recognizes the envelope-render intent only.
type fakeExecutor struct {
	gotRequest dispatch.ExecutorRequest
	resp       *dispatch.ExecutorResponse
	err        error
	called     bool
}

func (f *fakeExecutor) Intents() []string { return []string{"render_envelope"} }

func (f *fakeExecutor) Execute(ctx context.Context, req dispatch.ExecutorRequest) (*dispatch.ExecutorResponse, error) {
	f.called = true
	f.gotRequest = req
	return f.resp, f.err
}

// TestAttemptRouteDispatch_ChatDirectIsNoop covers the default route:
// when the classifier emits chat-direct, the seam logs and returns the
// zero outcome without dispatching anything.
func TestAttemptRouteDispatch_ChatDirectIsNoop(t *testing.T) {
	fe := &fakeExecutor{
		resp: &dispatch.ExecutorResponse{Summary: "should not be called"},
	}
	s := &chatServiceImpl{envelopeRenderExecutor: fe}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetRouteDecision(classify.RouteDecision{Route: classify.RouteChatDirect})

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptRouteDispatch(context.Background(), "session-1", "what's 2+2", ls, ch)
	close(ch)

	if fe.called {
		t.Errorf("expected fake executor NOT to be called for chat-direct route")
	}
	if out.Attempted {
		t.Errorf("Attempted = true, want false for chat-direct")
	}
	if out.Dispatched {
		t.Errorf("Dispatched = true, want false for chat-direct")
	}
	if got, want := drain(ch), 0; got != want {
		t.Errorf("emitted %d events, want %d for chat-direct", got, want)
	}
}

// TestAttemptRouteDispatch_NoExecutorWired covers the informative-only
// path: route is non-direct, but no executor is wired, so the seam
// logs and skips the dispatch.
func TestAttemptRouteDispatch_NoExecutorWired(t *testing.T) {
	s := &chatServiceImpl{envelopeRenderExecutor: nil}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetRouteDecision(classify.RouteDecision{Route: classify.RouteExecutorEnvelopeRender})

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptRouteDispatch(context.Background(), "session-1", "show me a card", ls, ch)
	close(ch)

	if out.Attempted {
		t.Errorf("Attempted = true, want false when executor is nil")
	}
	if got := drain(ch); got != 0 {
		t.Errorf("emitted %d events, want 0", got)
	}
}

// TestAttemptRouteDispatch_EnvelopeRenderSuccess covers the happy-path
// dispatch: route is executor_envelope_render, executor returns an
// envelope, the seam emits a plugin_envelope SSE event.
func TestAttemptRouteDispatch_EnvelopeRenderSuccess(t *testing.T) {
	fe := &fakeExecutor{
		resp: &dispatch.ExecutorResponse{
			Envelope: &dispatch.Envelope{
				Kind:    "envelope",
				Version: 1,
				Type:    "report-card",
				Data:    map[string]any{"title": "Sprint Metrics"},
			},
			Summary: "Rendered report-card.",
		},
	}
	s := &chatServiceImpl{envelopeRenderExecutor: fe}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetRouteDecision(classify.RouteDecision{
		Route:              classify.RouteExecutorEnvelopeRender,
		TargetEnvelopeType: "report-card",
		SyntheticAllowed:   true,
	})

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptRouteDispatch(context.Background(), "session-1", "show me a report-card", ls, ch)
	close(ch)

	if !fe.called {
		t.Fatalf("expected fake executor to be called")
	}
	if fe.gotRequest.Intent != "render_envelope" {
		t.Errorf("Intent = %q, want render_envelope", fe.gotRequest.Intent)
	}
	if fe.gotRequest.TargetEnvelopeType != "report-card" {
		t.Errorf("TargetEnvelopeType = %q, want report-card", fe.gotRequest.TargetEnvelopeType)
	}
	if fe.gotRequest.UserRequest != "show me a report-card" {
		t.Errorf("UserRequest = %q, want %q", fe.gotRequest.UserRequest, "show me a report-card")
	}
	if !fe.gotRequest.SyntheticAllowed {
		t.Errorf("SyntheticAllowed = false, want true")
	}
	if fe.gotRequest.SessionID != "session-1" {
		t.Errorf("SessionID = %q, want session-1", fe.gotRequest.SessionID)
	}

	if !out.Attempted {
		t.Errorf("Attempted = false, want true")
	}
	if !out.Dispatched {
		t.Errorf("Dispatched = false, want true")
	}
	if !out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = false, want true")
	}

	events := collect(ch)
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(events))
	}
	if events[0].Type != "plugin_envelope" {
		t.Errorf("event type = %q, want plugin_envelope", events[0].Type)
	}
	if !strings.Contains(events[0].Envelope, `"type":"report-card"`) {
		t.Errorf("envelope JSON missing type=report-card: %s", events[0].Envelope)
	}
	if !strings.Contains(events[0].Envelope, `"Sprint Metrics"`) {
		t.Errorf("envelope JSON missing data: %s", events[0].Envelope)
	}
}

// TestAttemptRouteDispatch_ExecutorFailureFallsBack covers the
// "informative not prohibitive" mitigation: when the executor returns
// a typed Failure (e.g. missing_context), the seam logs telemetry and
// returns Attempted=true / Dispatched=false. No envelope is emitted;
// the chat-direct LLM loop runs as normal afterward.
func TestAttemptRouteDispatch_ExecutorFailureFallsBack(t *testing.T) {
	fe := &fakeExecutor{
		resp: &dispatch.ExecutorResponse{
			Failure: &dispatch.ExecutorFailure{
				Code:    dispatch.ExecutorFailureMissingContext,
				Message: "data payload is required",
			},
			Summary: "Dispatch failed: no envelope data supplied.",
		},
	}
	s := &chatServiceImpl{envelopeRenderExecutor: fe}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetRouteDecision(classify.RouteDecision{
		Route:              classify.RouteExecutorEnvelopeRender,
		TargetEnvelopeType: "report-card",
	})

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptRouteDispatch(context.Background(), "session-1", "show me a report-card", ls, ch)
	close(ch)

	if !fe.called {
		t.Fatalf("expected fake executor to be called")
	}
	if !out.Attempted {
		t.Errorf("Attempted = false, want true")
	}
	if out.Dispatched {
		t.Errorf("Dispatched = true, want false on failure")
	}
	if out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = true, want false on failure")
	}
	if out.Response == nil || out.Response.Failure == nil {
		t.Fatalf("expected typed failure on outcome.Response")
	}
	if out.Response.Failure.Code != dispatch.ExecutorFailureMissingContext {
		t.Errorf("Failure.Code = %q, want missing_context", out.Response.Failure.Code)
	}
	if got := drain(ch); got != 0 {
		t.Errorf("emitted %d events, want 0 on failure", got)
	}
}

// TestAttemptRouteDispatch_DispatchExecutorError covers the harness
// wiring failure path — DispatchExecutor returns a non-nil error. The
// seam logs, marks Attempted=true, returns no envelope. (Reachable via
// a test-injected error from Execute; production callers see this
// only on misconfiguration.)
func TestAttemptRouteDispatch_DispatchExecutorError(t *testing.T) {
	fe := &fakeExecutor{
		err: errors.New("test-injected harness error"),
	}
	s := &chatServiceImpl{envelopeRenderExecutor: fe}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetRouteDecision(classify.RouteDecision{
		Route: classify.RouteExecutorEnvelopeRender,
	})

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptRouteDispatch(context.Background(), "session-1", "show me", ls, ch)
	close(ch)

	if !out.Attempted {
		t.Errorf("Attempted = false, want true even on harness error")
	}
	if out.Dispatched {
		t.Errorf("Dispatched = true, want false on harness error")
	}
}

// TestRouteToIntent covers the route-to-intent vocabulary mapping.
// Forward-compat: an unknown route returns "" so the dispatch seam
// can log + bail without dispatching with an empty intent.
func TestRouteToIntent(t *testing.T) {
	cases := []struct {
		r    classify.Route
		want string
	}{
		{classify.RouteExecutorEnvelopeRender, "render_envelope"},
		{classify.RouteChatDirect, ""},
		{classify.Route("unknown_future_route"), ""},
		{classify.Route(""), ""},
	}
	for _, c := range cases {
		if got := routeToIntent(c.r); got != c.want {
			t.Errorf("routeToIntent(%q) = %q, want %q", c.r, got, c.want)
		}
	}
}

// drain reads remaining events from ch and returns the count. ch must
// already be closed.
func drain(ch <-chan chat.StreamEvent) int {
	n := 0
	for range ch {
		n++
	}
	return n
}

// collect reads remaining events from ch into a slice. ch must already
// be closed.
func collect(ch <-chan chat.StreamEvent) []chat.StreamEvent {
	var out []chat.StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}
