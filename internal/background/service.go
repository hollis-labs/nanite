package background

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/messaging"
)

// Messenger is the narrow surface Service uses to deliver completion
// envelopes back to the originating session. *messaging.Service
// satisfies this structurally; tests inject a stub.
//
// Defined here (rather than reusing internal/messaging directly with
// its full surface) so test doubles stay minimal and the dependency
// boundary is explicit. This is the same pattern internal/subagent.MessagePoster
// uses for the same reason.
type Messenger interface {
	SendMessage(ctx context.Context, input messaging.SendInput) (*messaging.Message, error)
}

// jobRecord is the in-memory tracking entry for a single submitted
// job. Status transitions are guarded by Service.mu.
type jobRecord struct {
	id          string
	req         JobRequest
	status      JobStatus
	startedAt   time.Time
	completedAt time.Time
	output      string
	truncated   bool
	err         error
}

// Service orchestrates background-job submission, lifecycle tracking,
// and result delivery. Safe for concurrent use.
type Service struct {
	backend   Backend
	messenger Messenger

	// senderAgentID is the from_agent_id stamped on completion
	// envelopes. Conventionally a sentinel like "background-job"
	// rather than the originating agent — the message is from the
	// dispatch system, not the originating agent. Set at construction.
	senderAgentID string

	mu   sync.Mutex
	jobs map[string]*jobRecord
}

// SenderAgentID is the canonical from_agent_id stamped on the
// completion envelope. Other systems (subagent, inbox UI) recognize
// this slug to render the envelope correctly.
const SenderAgentID = "background-job"

// NewService constructs a Service. backend is the dispatch backend
// (PTY, mux, etc.); messenger delivers completion envelopes. Both
// are required for production wiring; nil values surface as Submit
// errors rather than panics.
func NewService(backend Backend, messenger Messenger) *Service {
	return &Service{
		backend:       backend,
		messenger:     messenger,
		senderAgentID: SenderAgentID,
		jobs:          make(map[string]*jobRecord),
	}
}

// Submit validates that pattern is PatternBackground (D5 gate),
// generates a stable job id, hands the request to the Backend, and
// tracks the job in-memory. Returns the job id; the result envelope
// arrives via messaging on completion.
//
// Validation errors (bad pattern, missing required fields, missing
// wiring) return immediately without touching the Backend.
func (svc *Service) Submit(ctx context.Context, pattern classify.ExecutionPattern, req JobRequest) (string, error) {
	if pattern != classify.PatternBackground {
		return "", fmt.Errorf("%w: got %q", ErrPatternNotBackground, pattern.String())
	}
	if svc.backend == nil {
		return "", ErrNoBackend
	}
	if svc.messenger == nil {
		return "", ErrNoMessenger
	}
	if strings.TrimSpace(req.Task) == "" {
		return "", fmt.Errorf("background: task is required")
	}
	if req.OriginatingSessionID == "" {
		return "", fmt.Errorf("background: originating_session_id is required")
	}
	if req.OriginatingAgentID == "" {
		return "", fmt.Errorf("background: originating_agent_id is required")
	}

	// Apply budget defaults.
	if req.Budget.WallClockSeconds <= 0 {
		req.Budget.WallClockSeconds = DefaultWallClockSeconds
	}
	if req.Budget.MaxOutputBytes <= 0 {
		req.Budget.MaxOutputBytes = DefaultMaxOutputBytes
	}

	jobID := uuid.New().String()

	svc.mu.Lock()
	svc.jobs[jobID] = &jobRecord{
		id:     jobID,
		req:    req,
		status: StatusPending,
	}
	svc.mu.Unlock()

	// Hand off to backend. The completion callback fires once when
	// the job reaches a terminal state — Service.onBackendComplete
	// updates the in-memory record and posts the messaging envelope.
	if err := svc.backend.Start(ctx, jobID, req, svc.onBackendComplete); err != nil {
		svc.mu.Lock()
		delete(svc.jobs, jobID)
		svc.mu.Unlock()
		return "", fmt.Errorf("background: backend start: %w", err)
	}

	// Backend.Start returned cleanly — flip to running. (Backends
	// may complete and call onBackendComplete before this lock
	// reacquires; the terminal-state guard in onBackendComplete
	// handles that race.)
	svc.mu.Lock()
	if rec, ok := svc.jobs[jobID]; ok && rec.status == StatusPending {
		rec.status = StatusRunning
		rec.startedAt = time.Now().UTC()
	}
	svc.mu.Unlock()

	return jobID, nil
}

// Status returns the current lifecycle state of jobID. ErrUnknownJob
// when no record exists.
func (svc *Service) Status(jobID string) (JobStatus, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	rec, ok := svc.jobs[jobID]
	if !ok {
		return "", ErrUnknownJob
	}
	return rec.status, nil
}

// Result returns the full result for a terminal job. Returns
// ErrUnknownJob for unknown ids; returns a JobResult with the current
// (possibly non-terminal) status for in-flight jobs so callers can
// poll without a status branch.
func (svc *Service) Result(jobID string) (JobResult, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	rec, ok := svc.jobs[jobID]
	if !ok {
		return JobResult{}, ErrUnknownJob
	}
	out := JobResult{
		JobID:           rec.id,
		Status:          rec.status,
		Output:          rec.output,
		StartedAt:       rec.startedAt,
		CompletedAt:     rec.completedAt,
		OutputTruncated: rec.truncated,
	}
	if rec.err != nil {
		out.Error = rec.err.Error()
	}
	return out, nil
}

// Cancel asks the Backend to terminate jobID. Idempotent — Cancel on
// an already-terminal or unknown job returns nil. The Backend must
// invoke the completion callback (with StatusCancelled) so the
// in-memory record + messaging envelope reflect the cancellation.
func (svc *Service) Cancel(jobID string) error {
	if svc.backend == nil {
		return ErrNoBackend
	}
	return svc.backend.Cancel(jobID)
}

// onBackendComplete is the callback Backend.Start invokes when a job
// reaches a terminal state. We update the in-memory record (under
// the mutex) and post the messaging envelope (outside the mutex, to
// keep messaging latency off the lock).
//
// The terminal-state guard ignores callbacks for jobs already marked
// terminal — defensive against backend bugs that fire the callback
// twice. Calling onComplete more than once per Start is a Backend
// implementation bug; we log and drop the duplicate rather than
// double-post the envelope.
func (svc *Service) onBackendComplete(jobID string, completion BackendCompletion) {
	svc.mu.Lock()
	rec, ok := svc.jobs[jobID]
	if !ok {
		svc.mu.Unlock()
		slog.Warn("background: completion for unknown job", "job_id", jobID)
		return
	}
	if rec.status.IsTerminal() {
		svc.mu.Unlock()
		slog.Warn("background: duplicate completion ignored",
			"job_id", jobID, "existing_status", string(rec.status))
		return
	}
	rec.status = completion.Status
	rec.output = completion.Output
	rec.truncated = completion.OutputTruncated
	rec.err = completion.Err
	if !completion.StartedAt.IsZero() {
		rec.startedAt = completion.StartedAt
	}
	rec.completedAt = completion.CompletedAt
	if rec.completedAt.IsZero() {
		rec.completedAt = time.Now().UTC()
	}
	req := rec.req
	finalResult := JobResult{
		JobID:           rec.id,
		Status:          rec.status,
		Output:          rec.output,
		StartedAt:       rec.startedAt,
		CompletedAt:     rec.completedAt,
		OutputTruncated: rec.truncated,
	}
	if rec.err != nil {
		finalResult.Error = rec.err.Error()
	}
	svc.mu.Unlock()

	svc.postCompletionEnvelope(req, finalResult)
}
