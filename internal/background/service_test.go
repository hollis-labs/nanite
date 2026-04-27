package background

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/messaging"
)

// stubMessenger captures every SendMessage call so tests can assert
// the completion envelope shape (kind, channel, subject, payload)
// without spinning up a real messaging.Service.
type stubMessenger struct {
	mu    sync.Mutex
	calls []messaging.SendInput
}

func (m *stubMessenger) SendMessage(_ context.Context, in messaging.SendInput) (*messaging.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, in)
	return &messaging.Message{ID: "stub-msg"}, nil
}

func (m *stubMessenger) captured() []messaging.SendInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]messaging.SendInput, len(m.calls))
	copy(out, m.calls)
	return out
}

// stubBackend is a programmable Backend test double. The test
// arranges the completion via stubBackend.complete; lifecycle
// transitions in the Service are observable without launching a
// real subprocess.
type stubBackend struct {
	mu sync.Mutex

	// startErr is returned by Start when set.
	startErr error

	// captured Start args
	starts []startCall

	// onComplete fns keyed by jobID. complete() pulls from here.
	completers map[string]CompletionFunc

	// status map for Status() lookups
	statuses map[string]JobStatus

	// cancelCalls records every Cancel invocation
	cancelCalls []string
}

type startCall struct {
	jobID string
	req   JobRequest
}

func newStubBackend() *stubBackend {
	return &stubBackend{
		completers: make(map[string]CompletionFunc),
		statuses:   make(map[string]JobStatus),
	}
}

func (b *stubBackend) Start(_ context.Context, jobID string, req JobRequest, onComplete CompletionFunc) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.startErr != nil {
		return b.startErr
	}
	b.starts = append(b.starts, startCall{jobID: jobID, req: req})
	b.completers[jobID] = onComplete
	b.statuses[jobID] = StatusRunning
	return nil
}

func (b *stubBackend) Status(jobID string) (JobStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.statuses[jobID]
	if !ok {
		return "", ErrUnknownJob
	}
	return s, nil
}

func (b *stubBackend) Cancel(jobID string) error {
	b.mu.Lock()
	b.cancelCalls = append(b.cancelCalls, jobID)
	cb := b.completers[jobID]
	b.statuses[jobID] = StatusCancelled
	b.mu.Unlock()
	if cb != nil {
		cb(jobID, BackendCompletion{
			Status:      StatusCancelled,
			StartedAt:   time.Now().UTC().Add(-100 * time.Millisecond),
			CompletedAt: time.Now().UTC(),
		})
	}
	return nil
}

// complete fires onComplete for jobID with the given completion.
// Used by tests to drive the Service through a transition.
func (b *stubBackend) complete(jobID string, c BackendCompletion) {
	b.mu.Lock()
	cb := b.completers[jobID]
	b.statuses[jobID] = c.Status
	b.mu.Unlock()
	if cb == nil {
		panic("stubBackend: complete called for unknown job " + jobID)
	}
	cb(jobID, c)
}

func validRequest() JobRequest {
	return JobRequest{
		Agent:                "researcher",
		Task:                 "echo hello",
		OriginatingSessionID: "sess-1",
		OriginatingAgentID:   "primary",
	}
}

func TestSubmit_RejectsNonBackgroundPattern(t *testing.T) {
	t.Parallel()
	cases := []classify.ExecutionPattern{
		classify.PatternInvalid,
		classify.PatternInline,
		classify.PatternSubagent,
	}
	for _, p := range cases {
		p := p
		t.Run(p.String(), func(t *testing.T) {
			t.Parallel()
			svc := NewService(newStubBackend(), &stubMessenger{})
			_, err := svc.Submit(context.Background(), p, validRequest())
			if err == nil {
				t.Fatalf("Submit(%s) expected error, got nil", p)
			}
			if !errors.Is(err, ErrPatternNotBackground) {
				t.Fatalf("Submit(%s) err = %v; want ErrPatternNotBackground", p, err)
			}
		})
	}
}

func TestSubmit_AcceptsBackgroundPattern(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	svc := NewService(be, &stubMessenger{})
	id, err := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if id == "" {
		t.Fatal("Submit returned empty job id")
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.starts) != 1 || be.starts[0].jobID != id {
		t.Fatalf("Backend.Start not invoked with returned id: %+v (want %s)", be.starts, id)
	}
}

func TestSubmit_RequiresWiringAndFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		svc     *Service
		req     JobRequest
		errPart string
	}{
		{"no backend", NewService(nil, &stubMessenger{}), validRequest(), "no backend"},
		{"no messenger", NewService(newStubBackend(), nil), validRequest(), "no messenger"},
		{"empty task", NewService(newStubBackend(), &stubMessenger{}), JobRequest{
			OriginatingSessionID: "s", OriginatingAgentID: "a",
		}, "task is required"},
		{"empty session", NewService(newStubBackend(), &stubMessenger{}), JobRequest{
			Task: "x", OriginatingAgentID: "a",
		}, "originating_session_id"},
		{"empty agent", NewService(newStubBackend(), &stubMessenger{}), JobRequest{
			Task: "x", OriginatingSessionID: "s",
		}, "originating_agent_id"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.svc.Submit(context.Background(), classify.PatternBackground, tc.req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("err = %v; want substring %q", err, tc.errPart)
			}
		})
	}
}

func TestLifecycle_StartRunningSucceeded(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	msgs := &stubMessenger{}
	svc := NewService(be, msgs)

	id, err := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if got, _ := svc.Status(id); got != StatusRunning {
		t.Fatalf("post-submit status = %s; want running", got)
	}

	be.complete(id, BackendCompletion{
		Status:      StatusSucceeded,
		Output:      "hello world",
		StartedAt:   time.Now().UTC().Add(-time.Second),
		CompletedAt: time.Now().UTC(),
	})

	if got, _ := svc.Status(id); got != StatusSucceeded {
		t.Fatalf("post-completion status = %s; want succeeded", got)
	}

	res, err := svc.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if res.Output != "hello world" {
		t.Fatalf("result output = %q; want hello world", res.Output)
	}
	if res.Status != StatusSucceeded {
		t.Fatalf("result status = %s; want succeeded", res.Status)
	}
}

func TestLifecycle_FailureSurfacesError(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	msgs := &stubMessenger{}
	svc := NewService(be, msgs)

	id, _ := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	be.complete(id, BackendCompletion{
		Status: StatusFailed,
		Output: "oops",
		Err:    errors.New("exit 1"),
	})

	res, _ := svc.Result(id)
	if res.Status != StatusFailed {
		t.Fatalf("status = %s; want failed", res.Status)
	}
	if !strings.Contains(res.Error, "exit 1") {
		t.Fatalf("error = %q; want substring exit 1", res.Error)
	}
}

func TestCancel_TransitionsToCancelled(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	msgs := &stubMessenger{}
	svc := NewService(be, msgs)

	id, _ := svc.Submit(context.Background(), classify.PatternBackground, validRequest())

	if err := svc.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	be.mu.Lock()
	if len(be.cancelCalls) != 1 || be.cancelCalls[0] != id {
		t.Fatalf("Backend.Cancel not invoked with %s: %+v", id, be.cancelCalls)
	}
	be.mu.Unlock()

	if got, _ := svc.Status(id); got != StatusCancelled {
		t.Fatalf("status after cancel = %s; want cancelled", got)
	}
}

func TestCancel_IsIdempotent(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	svc := NewService(be, &stubMessenger{})
	id, _ := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	be.complete(id, BackendCompletion{Status: StatusSucceeded})

	// Cancel after terminal — must not error.
	if err := svc.Cancel(id); err != nil {
		t.Fatalf("Cancel after terminal: %v", err)
	}
	// Cancel of unknown id — must not error (idempotent).
	if err := svc.Cancel("does-not-exist"); err != nil {
		t.Fatalf("Cancel unknown: %v", err)
	}
}

func TestStatus_UnknownReturnsErrUnknownJob(t *testing.T) {
	t.Parallel()
	svc := NewService(newStubBackend(), &stubMessenger{})
	_, err := svc.Status("nope")
	if !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("Status(unknown) = %v; want ErrUnknownJob", err)
	}
}

func TestMessagingRoundtrip_ShapeOnSuccess(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	msgs := &stubMessenger{}
	svc := NewService(be, msgs)

	req := validRequest()
	id, _ := svc.Submit(context.Background(), classify.PatternBackground, req)
	be.complete(id, BackendCompletion{
		Status:      StatusSucceeded,
		Output:      "captured-output",
		StartedAt:   time.Now().UTC().Add(-time.Second),
		CompletedAt: time.Now().UTC(),
	})

	calls := msgs.captured()
	if len(calls) != 1 {
		t.Fatalf("expected 1 messaging call, got %d", len(calls))
	}
	c := calls[0]
	if c.FromSessionID != req.OriginatingSessionID {
		t.Fatalf("from_session = %q; want %q", c.FromSessionID, req.OriginatingSessionID)
	}
	if c.FromAgentID != SenderAgentID {
		t.Fatalf("from_agent = %q; want %q", c.FromAgentID, SenderAgentID)
	}
	if c.ToSessionID != req.OriginatingSessionID {
		t.Fatalf("to_session = %q; want %q", c.ToSessionID, req.OriginatingSessionID)
	}
	if c.ToAgentID != req.OriginatingAgentID {
		t.Fatalf("to_agent = %q; want %q", c.ToAgentID, req.OriginatingAgentID)
	}
	if c.Channel != messaging.ChannelInbox {
		t.Fatalf("channel = %q; want %q", c.Channel, messaging.ChannelInbox)
	}
	if c.Kind != messaging.KindNotification {
		t.Fatalf("kind = %q; want %q", c.Kind, messaging.KindNotification)
	}
	if !strings.Contains(c.Subject, "succeeded") {
		t.Fatalf("subject = %q; want substring succeeded", c.Subject)
	}

	var payload JobResult
	if err := json.Unmarshal([]byte(c.PayloadJSON), &payload); err != nil {
		t.Fatalf("payload unmarshal: %v (raw=%q)", err, c.PayloadJSON)
	}
	if payload.JobID != id {
		t.Fatalf("payload job_id = %q; want %q", payload.JobID, id)
	}
	if payload.Status != StatusSucceeded {
		t.Fatalf("payload status = %s; want succeeded", payload.Status)
	}
	if payload.Output != "captured-output" {
		t.Fatalf("payload output = %q; want captured-output", payload.Output)
	}
}

func TestMessagingRoundtrip_ShapeOnFailure(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	msgs := &stubMessenger{}
	svc := NewService(be, msgs)

	id, _ := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	be.complete(id, BackendCompletion{
		Status: StatusFailed,
		Err:    errors.New("boom"),
	})

	calls := msgs.captured()
	if len(calls) != 1 {
		t.Fatalf("expected 1 messaging call, got %d", len(calls))
	}
	if !strings.Contains(calls[0].Subject, "failed") {
		t.Fatalf("subject = %q; want failed", calls[0].Subject)
	}
	if !strings.Contains(calls[0].Body, "boom") {
		t.Fatalf("body = %q; want boom", calls[0].Body)
	}
}

func TestDuplicateCompletion_Ignored(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	msgs := &stubMessenger{}
	svc := NewService(be, msgs)

	id, _ := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	be.complete(id, BackendCompletion{Status: StatusSucceeded, Output: "first"})
	be.complete(id, BackendCompletion{Status: StatusFailed, Output: "second"})

	if got := len(msgs.captured()); got != 1 {
		t.Fatalf("expected 1 messaging call (duplicate ignored), got %d", got)
	}
	res, _ := svc.Result(id)
	if res.Output != "first" {
		t.Fatalf("output = %q; want first", res.Output)
	}
}

func TestSubmit_BackendStartErrorRollsBack(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	be.startErr = errors.New("backend boom")
	svc := NewService(be, &stubMessenger{})

	id, err := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	if err == nil {
		t.Fatal("expected Submit error, got nil")
	}
	if id != "" {
		t.Fatalf("Submit returned id=%q; want empty on backend error", id)
	}
	// Status of any id should be ErrUnknownJob.
	_, statusErr := svc.Status("anything")
	if !errors.Is(statusErr, ErrUnknownJob) {
		t.Fatalf("Status after rollback err = %v; want ErrUnknownJob", statusErr)
	}
}

func TestBudgetDefaults_Applied(t *testing.T) {
	t.Parallel()
	be := newStubBackend()
	svc := NewService(be, &stubMessenger{})
	_, err := svc.Submit(context.Background(), classify.PatternBackground, validRequest())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	if got := be.starts[0].req.Budget.WallClockSeconds; got != DefaultWallClockSeconds {
		t.Fatalf("WallClockSeconds default not applied: got %d, want %d", got, DefaultWallClockSeconds)
	}
	if got := be.starts[0].req.Budget.MaxOutputBytes; got != DefaultMaxOutputBytes {
		t.Fatalf("MaxOutputBytes default not applied: got %d, want %d", got, DefaultMaxOutputBytes)
	}
}
