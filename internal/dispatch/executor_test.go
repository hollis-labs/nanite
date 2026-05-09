package dispatch

import (
	"context"
	"errors"
	"testing"
)

// fakeExecutor is a test seam for DispatchExecutor. It records the last
// request and returns a configurable response.
type fakeExecutor struct {
	intents []string
	resp    *ExecutorResponse
	err     error
	saw     ExecutorRequest
	calls   int
}

func (f *fakeExecutor) Intents() []string { return f.intents }

func (f *fakeExecutor) Execute(_ context.Context, req ExecutorRequest) (*ExecutorResponse, error) {
	f.saw = req
	f.calls++
	return f.resp, f.err
}

// TestDispatchExecutor_RoutesKnownIntent passes a registered intent
// through to the Executor.Execute call and returns the response unchanged.
func TestDispatchExecutor_RoutesKnownIntent(t *testing.T) {
	want := &ExecutorResponse{
		Summary: "ok",
		Envelope: &Envelope{
			Kind: "envelope", Version: 1, Type: "report-card",
		},
	}
	fe := &fakeExecutor{
		intents: []string{"render_envelope"},
		resp:    want,
	}
	got, err := DispatchExecutor(context.Background(), fe, ExecutorRequest{
		Intent:             "render_envelope",
		TargetEnvelopeType: "report-card",
	})
	if err != nil {
		t.Fatalf("DispatchExecutor: %v", err)
	}
	if got != want {
		t.Errorf("expected response pass-through, got %+v", got)
	}
	if fe.calls != 1 {
		t.Errorf("expected one Execute call, got %d", fe.calls)
	}
}

// TestDispatchExecutor_UnknownIntent_FailsClosed returns an
// invalid_intent failure rather than calling Execute.
func TestDispatchExecutor_UnknownIntent_FailsClosed(t *testing.T) {
	fe := &fakeExecutor{intents: []string{"render_envelope"}}
	resp, err := DispatchExecutor(context.Background(), fe, ExecutorRequest{
		Intent: "knowledge_grounded_answer",
	})
	if err != nil {
		t.Fatalf("DispatchExecutor: %v", err)
	}
	if resp == nil || resp.Failure == nil {
		t.Fatal("expected typed failure response")
	}
	if resp.Failure.Code != ExecutorFailureInvalidIntent {
		t.Fatalf("expected invalid_intent, got %q", resp.Failure.Code)
	}
	if fe.calls != 0 {
		t.Errorf("Execute must not be called for unknown intents (calls=%d)", fe.calls)
	}
}

// TestDispatchExecutor_EmptyIntent_FailsClosed mirrors the above for
// the empty-intent guard.
func TestDispatchExecutor_EmptyIntent_FailsClosed(t *testing.T) {
	fe := &fakeExecutor{intents: []string{"render_envelope"}}
	resp, err := DispatchExecutor(context.Background(), fe, ExecutorRequest{})
	if err != nil {
		t.Fatalf("DispatchExecutor: %v", err)
	}
	if resp == nil || resp.Failure == nil || resp.Failure.Code != ExecutorFailureInvalidIntent {
		t.Fatalf("expected invalid_intent failure, got %+v", resp)
	}
}

// TestDispatchExecutor_NilExecutor_HarnessError reports a harness-level
// error (not an agent-visible failure) when the wiring layer didn't
// register an executor.
func TestDispatchExecutor_NilExecutor_HarnessError(t *testing.T) {
	resp, err := DispatchExecutor(context.Background(), nil, ExecutorRequest{
		Intent: "render_envelope",
	})
	if err == nil {
		t.Fatal("expected error for nil executor")
	}
	if resp != nil {
		t.Errorf("expected nil response on harness error, got %+v", resp)
	}
}

// TestExecutorFailureCode_StringRepresentation guards against accidental
// rename of typed failure codes — these strings are observable in
// telemetry and (eventually) on the wire.
func TestExecutorFailureCode_StringRepresentation(t *testing.T) {
	cases := map[ExecutorFailureCode]string{
		ExecutorFailureBudgetExhausted: "budget_exhausted",
		ExecutorFailureUnrecoverable:   "unrecoverable",
		ExecutorFailureInvalidIntent:   "invalid_intent",
		ExecutorFailureMissingContext:  "missing_context",
		ExecutorFailureTrustDenied:     "trust_denied",
	}
	for code, want := range cases {
		if string(code) != want {
			t.Errorf("ExecutorFailureCode %q: got %q, want %q", want, string(code), want)
		}
	}
}

// TestDispatchExecutor_ExecutorReturnsError_PropagatesError covers the
// "harness wiring problem" path: when an executor returns a non-nil Go
// error, DispatchExecutor passes it through (does NOT wrap it as a
// typed failure — the Executor surface reserves the error return for
// wiring problems, and turning those into agent-visible failures
// silently would mask bugs).
func TestDispatchExecutor_ExecutorReturnsError_PropagatesError(t *testing.T) {
	want := errors.New("wiring fault")
	fe := &fakeExecutor{
		intents: []string{"render_envelope"},
		err:     want,
	}
	_, err := DispatchExecutor(context.Background(), fe, ExecutorRequest{
		Intent: "render_envelope",
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped wiring error, got %v", err)
	}
}
