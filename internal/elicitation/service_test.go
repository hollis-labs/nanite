package elicitation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/elicitation"
)

// stubEmitter records emits and allows tests to control the emit result.
type stubEmitter struct {
	mu      sync.Mutex
	emitErr error
	emitted []elicitation.EmitInput
}

func (s *stubEmitter) EmitElicitationPrompt(_ context.Context, input elicitation.EmitInput) (string, error) {
	s.mu.Lock()
	s.emitted = append(s.emitted, input)
	s.mu.Unlock()
	return "env-instance-id-1", s.emitErr
}

func (s *stubEmitter) getEmitted() []elicitation.EmitInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]elicitation.EmitInput, len(s.emitted))
	copy(cp, s.emitted)
	return cp
}

func (s *stubEmitter) waitEmit(t *testing.T, count int, timeout time.Duration) []elicitation.EmitInput {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if em := s.getEmitted(); len(em) >= count {
			return em
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d emits", count)
	return nil
}

// TestElicit_AcceptString exercises the happy-path where a user submits a
// string-type response.
func TestElicit_AcceptString(t *testing.T) {
	emitter := &stubEmitter{}
	svc := elicitation.New(emitter, 10) // 10 s timeout — plenty for a test

	// Respond asynchronously (simulating UI interaction).
	// We use a done channel to synchronize the goroutine with the test.
	done := make(chan struct{})
	go func() {
		defer close(done)
		em := emitter.waitEmit(t, 1, 5*time.Second)
		id := em[0].Data.ElicitationID
		if err := svc.Respond(id, elicitation.Response{
			Action:  elicitation.ActionAccept,
			Content: "my answer",
		}); err != nil {
			t.Errorf("Respond error: %v", err)
		}
	}()

	resp, err := svc.Elicit(context.Background(), elicitation.ElicitInput{
		Message:    "What should I do?",
		Schema:     &elicitation.RequestedSchema{Type: elicitation.SchemaTypeString},
		SessionID:  "sess-1",
		AgentID:    "agent-1",
		Origin:     "server",
		ToolCallID: "tc-001",
	})
	if err != nil {
		t.Fatalf("Elicit error: %v", err)
	}
	if resp.Action != elicitation.ActionAccept {
		t.Errorf("action: got %q, want %q", resp.Action, elicitation.ActionAccept)
	}
	if resp.Content != "my answer" {
		t.Errorf("content: got %q, want %q", resp.Content, "my answer")
	}

	<-done // wait for goroutine to finish

	// Verify the envelope was emitted.
	emitted := emitter.getEmitted()
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(emitted))
	}
	emit := emitted[0]
	if emit.SessionID != "sess-1" {
		t.Errorf("emit SessionID: got %q, want %q", emit.SessionID, "sess-1")
	}
	if emit.Data.SchemaType != "string" {
		t.Errorf("emit schema_type: got %q, want %q", emit.Data.SchemaType, "string")
	}
	if emit.Data.ToolCallID != "tc-001" {
		t.Errorf("emit tool_call_id: got %q, want %q", emit.Data.ToolCallID, "tc-001")
	}
}

// TestElicit_DeclineBoolean exercises a boolean schema where user declines.
func TestElicit_DeclineBoolean(t *testing.T) {
	emitter := &stubEmitter{}
	svc := elicitation.New(emitter, 10)

	go func() {
		em := emitter.waitEmit(t, 1, 5*time.Second)
		id := em[0].Data.ElicitationID
		_ = svc.Respond(id, elicitation.Response{Action: elicitation.ActionDecline})
	}()

	resp, err := svc.Elicit(context.Background(), elicitation.ElicitInput{
		Message:   "Are you sure you want to delete?",
		Schema:    &elicitation.RequestedSchema{Type: elicitation.SchemaTypeBoolean},
		SessionID: "sess-2",
		AgentID:   "agent-1",
		Origin:    "server",
	})
	if err != nil {
		t.Fatalf("Elicit error: %v", err)
	}
	if resp.Action != elicitation.ActionDecline {
		t.Errorf("action: got %q, want %q", resp.Action, elicitation.ActionDecline)
	}
}

// TestElicit_Timeout verifies that auto-cancel fires after the timeout with reason=timeout.
func TestElicit_Timeout(t *testing.T) {
	emitter := &stubEmitter{}
	// 100 ms timeout — fast enough for tests, per ticket spec.
	svc := elicitation.New(emitter, 0)
	// Override via env is not convenient in test; pass a tiny positive value
	// by using a helper that accepts milliseconds. We'll use the 1-second
	// minimum because New clamps ≤0 to default. Instead, we build with 1 and
	// verify the action=cancel path with a context timeout that's shorter
	// than the service timeout but longer than the test wait — but the easiest
	// approach is to test the context-canceled path instead.
	//
	// Actually: create service with 1s timeout and use a context that cancels
	// immediately so we get action=cancel with reason=context_canceled.
	// That exercises the same code path the timeout goroutine uses.
	svcFast := newFastSvc(emitter, 100*time.Millisecond)

	ctx := context.Background()
	resp, err := svcFast.Elicit(ctx, elicitation.ElicitInput{
		Message:   "Will you respond?",
		Schema:    &elicitation.RequestedSchema{Type: elicitation.SchemaTypeBoolean},
		SessionID: "sess-3",
		AgentID:   "agent-1",
		Origin:    "server",
	})
	// No one responds — service auto-cancels after 100 ms.
	if err != nil {
		t.Logf("Elicit returned error (expected for context cancel): %v", err)
	}
	if resp.Action != elicitation.ActionCancel {
		t.Errorf("action: got %q, want %q", resp.Action, elicitation.ActionCancel)
	}
	if resp.Reason != "timeout" {
		t.Errorf("reason: got %q, want %q", resp.Reason, "timeout")
	}
	_ = svc
}

// TestElicit_ContextCanceled verifies ctx cancellation returns action=cancel.
func TestElicit_ContextCanceled(t *testing.T) {
	emitter := &stubEmitter{}
	svc := elicitation.New(emitter, 60)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	resp, err := svc.Elicit(ctx, elicitation.ElicitInput{
		Message:   "Will you respond?",
		Schema:    nil,
		SessionID: "sess-4",
		AgentID:   "agent-1",
		Origin:    "client",
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
	if resp.Action != elicitation.ActionCancel {
		t.Errorf("action: got %q, want %q", resp.Action, elicitation.ActionCancel)
	}
	if resp.Reason != "context_canceled" {
		t.Errorf("reason: got %q, want %q", resp.Reason, "context_canceled")
	}
}

// TestElicit_Respond_UnknownID verifies Respond returns error for unknown IDs.
func TestElicit_Respond_UnknownID(t *testing.T) {
	emitter := &stubEmitter{}
	svc := elicitation.New(emitter, 10)

	err := svc.Respond("nonexistent-id", elicitation.Response{Action: elicitation.ActionAccept})
	if err == nil {
		t.Error("expected error for unknown elicitation ID, got nil")
	}
}

// TestRespondFromEnvelopeData verifies the envelope response parsing path.
func TestRespondFromEnvelopeData(t *testing.T) {
	emitter := &stubEmitter{}
	svc := elicitation.New(emitter, 10)

	go func() {
		em := emitter.waitEmit(t, 1, 5*time.Second)
		id := em[0].Data.ElicitationID
		err := svc.RespondFromEnvelopeData(map[string]any{
			"elicitation_id": id,
			"action":         "accept",
			"content":        "confirmed",
		})
		if err != nil {
			t.Errorf("RespondFromEnvelopeData error: %v", err)
		}
	}()

	resp, err := svc.Elicit(context.Background(), elicitation.ElicitInput{
		Message:   "Confirm action?",
		Schema:    &elicitation.RequestedSchema{Type: elicitation.SchemaTypeString},
		SessionID: "sess-5",
		AgentID:   "agent-1",
		Origin:    "server",
	})
	if err != nil {
		t.Fatalf("Elicit error: %v", err)
	}
	if resp.Action != elicitation.ActionAccept {
		t.Errorf("action: got %q, want %q", resp.Action, elicitation.ActionAccept)
	}
	if resp.Content != "confirmed" {
		t.Errorf("content: got %q, want %q", resp.Content, "confirmed")
	}
}

// TestValidateResponse_SchemaValidation tests the schema validation helper.
func TestValidateResponse_SchemaValidation(t *testing.T) {
	boolSchema := &elicitation.RequestedSchema{Type: elicitation.SchemaTypeBoolean}
	stringSchema := &elicitation.RequestedSchema{Type: elicitation.SchemaTypeString}

	// Boolean accept — no content required.
	if err := elicitation.ValidateResponse(boolSchema, elicitation.Response{Action: elicitation.ActionAccept}); err != nil {
		t.Errorf("bool accept: unexpected error: %v", err)
	}

	// String accept requires content.
	err := elicitation.ValidateResponse(stringSchema, elicitation.Response{Action: elicitation.ActionAccept, Content: ""})
	if err == nil {
		t.Error("string accept with empty content: expected error, got nil")
	}

	// String accept with content — ok.
	if err := elicitation.ValidateResponse(stringSchema, elicitation.Response{Action: elicitation.ActionAccept, Content: "hello"}); err != nil {
		t.Errorf("string accept with content: unexpected error: %v", err)
	}

	// Decline — no validation needed regardless of schema.
	if err := elicitation.ValidateResponse(stringSchema, elicitation.Response{Action: elicitation.ActionDecline}); err != nil {
		t.Errorf("decline: unexpected error: %v", err)
	}
}

// TestElicit_EmitError verifies that an emit failure returns action=cancel.
func TestElicit_EmitError(t *testing.T) {
	emitter := &stubEmitter{emitErr: errors.New("broker down")}
	svc := elicitation.New(emitter, 10)

	resp, err := svc.Elicit(context.Background(), elicitation.ElicitInput{
		Message:   "Can you hear me?",
		SessionID: "sess-6",
		AgentID:   "agent-1",
		Origin:    "server",
	})
	if err == nil {
		t.Error("expected error from emit failure, got nil")
	}
	if resp.Action != elicitation.ActionCancel {
		t.Errorf("action: got %q, want %q", resp.Action, elicitation.ActionCancel)
	}
	if resp.Reason != "emit_failed" {
		t.Errorf("reason: got %q, want %q", resp.Reason, "emit_failed")
	}
}

// newFastSvc constructs a Service with a sub-second timeout for test scenarios.
// We use the internal timeout mechanism by wrapping the service constructor
// with a 1-second floor — but in tests we want sub-second. The cleanest
// approach is to set NANITE_ELICITATION_TIMEOUT_SEC before calling New, then
// restore. Alternatively we expose a test helper in the package.
//
// For simplicity we use the context-cancel approach which exercises the same
// select branch. Here we want to test the *timeout* branch, so we set a very
// short timeout via env.
func newFastSvc(emitter elicitation.Emitter, d time.Duration) *elicitation.ServiceForTest {
	return elicitation.NewForTest(emitter, d)
}
