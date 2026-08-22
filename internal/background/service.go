package background

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sort"
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

	mu          sync.Mutex
	jobs        map[string]*jobRecord
	jobIDSecret [sha256.Size]byte
	retention   completedJobRetention
}

// completedJobRetention bounds terminal result records. A day gives
// poll-based consumers a conservative retrieval window, while the hard
// count prevents a burst from retaining more than roughly 100 MiB of
// captured output (each result is independently capped at 1 MiB).
type completedJobRetention struct {
	ttl          time.Duration
	maxCompleted int
	now          func() time.Time
}

const (
	defaultCompletedJobTTL          = 24 * time.Hour
	defaultMaxRetainedCompletedJobs = 100
	jobIDTokenVersion               = "bg1"
)

var jobIDTagEncoding = base64.RawURLEncoding.Strict()

// SenderAgentID is the canonical from_agent_id stamped on the
// completion envelope. Other systems (subagent, inbox UI) recognize
// this slug to render the envelope correctly.
const SenderAgentID = "background-job"

// NewService constructs a Service. backend is the dispatch backend
// (PTY, mux, etc.); messenger delivers completion envelopes. Both
// are required for production wiring; nil values surface as Submit
// errors rather than panics.
func NewService(backend Backend, messenger Messenger) *Service {
	return newServiceWithRetention(backend, messenger, completedJobRetention{
		ttl:          defaultCompletedJobTTL,
		maxCompleted: defaultMaxRetainedCompletedJobs,
		now:          time.Now,
	})
}

// newServiceWithRetention is the deterministic test seam for retention.
// Production always uses the conservative defaults in NewService.
func newServiceWithRetention(backend Backend, messenger Messenger, retention completedJobRetention) *Service {
	if retention.now == nil {
		retention.now = time.Now
	}
	return &Service{
		backend:       backend,
		messenger:     messenger,
		senderAgentID: SenderAgentID,
		jobs:          make(map[string]*jobRecord),
		jobIDSecret:   newJobIDSecret(),
		retention:     retention,
	}
}

func newJobIDSecret() [sha256.Size]byte {
	var secret [sha256.Size]byte
	if _, err := rand.Read(secret[:]); err != nil {
		// NewService cannot return an error without breaking its public API.
		// Continuing without a secret would collapse expired-vs-unknown
		// integrity, so fail closed during construction instead.
		panic(fmt.Sprintf("background: generate job id secret: %v", err))
	}
	return secret
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

	svc.mu.Lock()
	svc.pruneCompletedLocked(svc.now())
	jobID := svc.newJobID()
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

// Status returns the current lifecycle state of jobID. A known job whose
// terminal record was evicted returns StatusExpired with ErrExpiredJob;
// an id never issued by this service instance returns ErrUnknownJob.
func (svc *Service) Status(jobID string) (JobStatus, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.pruneCompletedLocked(svc.now())
	rec, ok := svc.jobs[jobID]
	if !ok {
		return svc.missingJobResultLocked(jobID)
	}
	return rec.status, nil
}

// Result returns the full result for a terminal job. Returns
// ErrExpiredJob with StatusExpired for an evicted result, ErrUnknownJob
// for an id never issued by this service instance, or a JobResult with
// the current (possibly non-terminal) status for an in-flight job.
func (svc *Service) Result(jobID string) (JobResult, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.pruneCompletedLocked(svc.now())
	rec, ok := svc.jobs[jobID]
	if !ok {
		if svc.isIssuedJobIDLocked(jobID) {
			return JobResult{JobID: jobID, Status: StatusExpired}, ErrExpiredJob
		}
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
		rec.completedAt = svc.now()
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
	svc.pruneCompletedLocked(svc.now())
	svc.mu.Unlock()

	svc.postCompletionEnvelope(req, finalResult)
}

func (svc *Service) now() time.Time {
	return svc.retention.now().UTC()
}

// missingJobResultLocked distinguishes an evicted job from an arbitrary id
// without retaining an unbounded tombstone map. Job ids carry an HMAC made
// with a random per-Service secret, so an absent authenticated id was issued by
// this Service and has since left the registry. The secret is never present in
// the token, and replacing the Service establishes the in-memory restart
// boundary: tokens from an earlier instance no longer authenticate.
func (svc *Service) missingJobResultLocked(jobID string) (JobStatus, error) {
	if svc.isIssuedJobIDLocked(jobID) {
		return StatusExpired, ErrExpiredJob
	}
	return "", ErrUnknownJob
}

func (svc *Service) isIssuedJobIDLocked(jobID string) bool {
	version, remainder, ok := strings.Cut(jobID, ":")
	if !ok || version != jobIDTokenVersion {
		return false
	}
	nonceText, tagText, ok := strings.Cut(remainder, ":")
	if !ok || strings.Contains(tagText, ":") {
		return false
	}
	nonce, err := uuid.Parse(nonceText)
	if err != nil || nonce.String() != nonceText {
		return false
	}
	if len(tagText) != jobIDTagEncoding.EncodedLen(sha256.Size) {
		return false
	}
	tag, err := jobIDTagEncoding.DecodeString(tagText)
	if err != nil || len(tag) != sha256.Size {
		return false
	}
	if jobIDTagEncoding.EncodeToString(tag) != tagText {
		return false
	}
	payload := version + ":" + nonceText
	return hmac.Equal(tag, svc.signJobID(payload))
}

func (svc *Service) newJobID() string {
	payload := jobIDTokenVersion + ":" + uuid.NewString()
	tag := jobIDTagEncoding.EncodeToString(svc.signJobID(payload))
	return payload + ":" + tag
}

func (svc *Service) signJobID(payload string) []byte {
	mac := hmac.New(sha256.New, svc.jobIDSecret[:])
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// pruneCompletedLocked applies TTL first, then evicts the oldest completed
// records until the retained completed count is within the hard ceiling.
// Pending and running jobs are never candidates, even when they are older
// than the TTL or the completed-result cap is already full.
func (svc *Service) pruneCompletedLocked(now time.Time) {
	type completedRecord struct {
		id          string
		completedAt time.Time
	}

	completed := make([]completedRecord, 0, len(svc.jobs))
	for id, rec := range svc.jobs {
		if !rec.status.IsTerminal() {
			continue
		}
		if svc.retention.ttl > 0 && !rec.completedAt.IsZero() && !now.Before(rec.completedAt.Add(svc.retention.ttl)) {
			delete(svc.jobs, id)
			continue
		}
		completed = append(completed, completedRecord{id: id, completedAt: rec.completedAt})
	}

	if svc.retention.maxCompleted < 0 || len(completed) <= svc.retention.maxCompleted {
		return
	}
	sort.Slice(completed, func(i, j int) bool {
		if completed[i].completedAt.Equal(completed[j].completedAt) {
			return completed[i].id < completed[j].id
		}
		return completed[i].completedAt.Before(completed[j].completedAt)
	})
	for _, rec := range completed[:len(completed)-svc.retention.maxCompleted] {
		delete(svc.jobs, rec.id)
	}
}
