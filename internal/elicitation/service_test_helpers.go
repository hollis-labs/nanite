package elicitation

import (
	"context"
	"time"
)

// ServiceForTest is a thin wrapper around Service that accepts a time.Duration
// timeout directly. This lets tests exercise sub-second timeouts without
// manipulating environment variables.
//
// Not part of the production API — _test.go callers reference this via the
// package-internal alias exposed below.
type ServiceForTest struct {
	emitter        Emitter
	timeoutDur     time.Duration
	inner          *Service // carries the pending map
}

// NewForTest constructs a Service with an explicit duration timeout.
// Exported so test files in package elicitation_test can construct it.
func NewForTest(emitter Emitter, timeout time.Duration) *ServiceForTest {
	svc := &Service{
		emitter:        emitter,
		timeoutSeconds: int(timeout.Seconds()),
		pending:        make(map[string]*Request),
	}
	return &ServiceForTest{
		emitter:    emitter,
		timeoutDur: timeout,
		inner:      svc,
	}
}

// Elicit delegates to the inner service but uses the explicit duration.
func (s *ServiceForTest) Elicit(ctx context.Context, req ElicitInput) (Response, error) {
	// Re-implement Elicit with the exact duration instead of int seconds
	// so sub-second values work correctly in tests.
	return elicitWithDuration(ctx, s.inner, req, s.timeoutDur)
}

// elicitWithDuration is an internal helper that runs the same logic as
// Service.Elicit but accepts a time.Duration instead of converting from int.
func elicitWithDuration(ctx context.Context, s *Service, req ElicitInput, timeout time.Duration) (Response, error) {
	id := newID()
	schemaType := SchemaTypeString
	if req.Schema != nil && req.Schema.Type != "" {
		schemaType = req.Schema.Type
	}

	pending := &Request{
		ID:         id,
		Message:    req.Message,
		Schema:     req.Schema,
		ToolCallID: req.ToolCallID,
		SessionID:  req.SessionID,
		AgentID:    req.AgentID,
		Origin:     req.Origin,
		CreatedAt:  time.Now(),
		result:     make(chan Response, 1),
	}

	s.mu.Lock()
	s.pending[id] = pending
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	data := EnvelopeData{
		ElicitationID: id,
		Message:       req.Message,
		SchemaType:    string(schemaType),
		ToolCallID:    req.ToolCallID,
		Origin:        req.Origin,
		TimeoutAt:     time.Now().Add(timeout),
	}
	if req.Schema != nil {
		data.SchemaTitle = req.Schema.Title
		data.SchemaDescription = req.Schema.Description
	}
	if _, err := s.emitter.EmitElicitationPrompt(ctx, EmitInput{
		SessionID: req.SessionID,
		AgentID:   req.AgentID,
		Data:      data,
	}); err != nil {
		return Response{Action: ActionCancel, Reason: "emit_failed"}, errElicitEmit(err)
	}

	select {
	case resp := <-pending.result:
		return resp, nil
	case <-time.After(timeout):
		return Response{Action: ActionCancel, Reason: "timeout"}, nil
	case <-ctx.Done():
		return Response{Action: ActionCancel, Reason: "context_cancelled"}, ctx.Err()
	}
}
