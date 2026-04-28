// Package background implements the P9 BackgroundJob primitive
// (CW-20260420-0016): async, non-session-bound dispatch for work that
// should not block the originating session.
//
// Architectural placement (three-way contrast with sibling primitives):
//
//   - peer_query (P5)         : sync,   in-session.        Lightest.
//   - spawn_subagent          : sync/async, in-session.    Medium.
//   - background_job (this)   : async,  non-session-bound. Heaviest;
//     reply lands via the messaging system.
//
// Decision pins (do NOT reopen — see CW-20260420-0016):
//
//   - D1 — Hadron is NOT the dispatch primitive. Zero `hadron` imports
//     in this package. Re-violating is an explicit blocker.
//   - D2 — Short-term dispatch is a detached PTY session (see pty.go).
//     This is the MVP that ships in P9.
//   - D3 — Long-term dispatch swaps to agent-mux behind the Backend
//     interface defined here. Activation is a separate ticket; this
//     package must NOT import or call agent-mux.
//   - D4 — Result delivery uses the existing messaging system
//     (internal/messaging). Result posts back to the originating
//     session as a notification on completion.
//   - D5 — classify.PatternBackground is the gate. Service.Submit
//     rejects anything other than PatternBackground.
//   - D6 — Future consumers (Strategy loop, Playbook, Mux dev-mode)
//     are out of scope for this ticket; the seam is preserved.
//
// In-memory only — job state is not persisted across process restarts
// in this MVP. A persistence layer is a follow-up if a real consumer
// needs it (see ticket Out-of-scope).
package background

import (
	"context"
	"time"
)

// JobStatus is the lifecycle state of a background job.
//
// Lifecycle:
//
//	pending → running → succeeded | failed | cancelled
//
// `pending` is a brief window between Submit and the Backend's Start
// returning a job_id. Once Start returns, the service flips to running
// and tracks the job until Backend completion or Cancel.
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

// IsTerminal reports whether s is a terminal lifecycle state.
func (s JobStatus) IsTerminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// JobBudget bounds a job's runtime + output. Zero values mean "use
// the package default" — Service.Submit fills defaults before passing
// to the Backend.
type JobBudget struct {
	// WallClockSeconds caps total wall-clock runtime. 0 = use default
	// (DefaultWallClockSeconds).
	WallClockSeconds int
	// MaxOutputBytes caps captured stdout+stderr. 0 = use default
	// (DefaultMaxOutputBytes). Output is truncated, not failed, on
	// overflow.
	MaxOutputBytes int
}

// Defaults applied to JobBudget zero fields.
const (
	// DefaultWallClockSeconds bounds the longest-running job we'll
	// allow before the backend cancels it. 30 minutes is enough for
	// the codebase-crawl / multi-file-research use cases the ticket
	// calls out without leaving forgotten processes orphaned for hours.
	DefaultWallClockSeconds = 30 * 60
	// DefaultMaxOutputBytes caps captured output at 1 MiB. Larger
	// outputs are truncated; the result envelope's Output field marks
	// the truncation explicitly.
	DefaultMaxOutputBytes = 1 << 20
)

// JobRequest is the caller-supplied input to Submit.
type JobRequest struct {
	// Agent is the agent slug (e.g. role) the backend boots to handle
	// the task. The Backend implementation owns how this maps to a
	// concrete process invocation.
	Agent string
	// Task is the prompt / instruction text the spawned process is
	// given. Opaque to this package; the Backend wires it into its
	// process invocation.
	Task string
	// Budget bounds the job. Zero fields fall back to package defaults
	// (see Defaults above).
	Budget JobBudget
	// OriginatingSessionID is the session that submitted the job.
	// The completion envelope is delivered here.
	OriginatingSessionID string
	// OriginatingAgentID is the agent that submitted the job.
	// Conventionally the to_agent on the completion envelope.
	OriginatingAgentID string
}

// JobResult is the structured payload posted back via messaging when
// the job reaches a terminal state.
type JobResult struct {
	JobID       string    `json:"job_id"`
	Status      JobStatus `json:"status"`
	Output      string    `json:"output"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	// OutputTruncated reports whether captured output was clipped at
	// MaxOutputBytes. When true, Output ends with a trailing newline
	// and a "[output truncated at N bytes]" sentinel; consumers that
	// need the full transcript should re-run with a larger budget.
	OutputTruncated bool `json:"output_truncated,omitempty"`
}

// Backend is the dispatch surface Service uses to actually run jobs.
// Two implementations are anticipated:
//
//   - PTYBackend (this package, pty.go)   — short-term MVP (D2).
//   - mux backend (separate package)      — long-term (D3); not in
//     this ticket. The interface stays narrow so that swap is a
//     wiring change, not an API change.
//
// Implementations MUST:
//
//   - Honor ctx cancellation in Start (long-running setup steps must
//     bail when ctx is done).
//   - Make Cancel idempotent (a no-op for unknown or already-terminal
//     jobs is acceptable as long as it returns nil; an actual error
//     is reserved for backend-state corruption).
//   - Clean up child processes on Cancel — no orphans.
type Backend interface {
	// Start launches a new job and returns the backend-assigned job
	// id (which Service treats as opaque). The Backend is responsible
	// for invoking onComplete exactly once when the job reaches a
	// terminal state — onComplete carries the captured output, exit
	// status, and timestamps.
	//
	// Start should return quickly (it does not block until job
	// completion); the Backend runs the job in a goroutine and fires
	// onComplete asynchronously.
	Start(ctx context.Context, jobID string, req JobRequest, onComplete CompletionFunc) error

	// Status returns the current lifecycle state of jobID. Returns
	// ErrUnknownJob when the backend has no record of jobID.
	Status(jobID string) (JobStatus, error)

	// Cancel attempts to terminate jobID. Returns nil for already-
	// terminal or unknown jobs (idempotent). Implementations must
	// reap the child process and any descendants — orphans are
	// blockers.
	Cancel(jobID string) error
}

// CompletionFunc is the callback the Backend invokes when a job
// reaches a terminal state. The Service uses it to update its
// in-memory state and post the messaging envelope. jobID echoes
// back the id Service.Submit handed to Backend.Start, so a single
// callback can dispatch results for many jobs.
//
// Backends MUST invoke CompletionFunc exactly once per Start call.
// Calling it more than once or never is a Backend implementation bug.
type CompletionFunc func(jobID string, completion BackendCompletion)

// BackendCompletion is what Backend reports to the Service via the
// CompletionFunc callback. The Service translates this into a
// JobResult before posting the messaging envelope.
type BackendCompletion struct {
	// Status is one of StatusSucceeded, StatusFailed, StatusCancelled.
	Status JobStatus
	// Output is captured stdout+stderr (interleaved or merged is
	// implementation-defined).
	Output string
	// OutputTruncated reports whether Output was clipped at
	// MaxOutputBytes.
	OutputTruncated bool
	// Err carries the failure reason for StatusFailed; nil for
	// StatusSucceeded and StatusCancelled (cancellation is a request,
	// not an error).
	Err error
	// StartedAt and CompletedAt bound the job's wall-clock runtime.
	StartedAt   time.Time
	CompletedAt time.Time
}
