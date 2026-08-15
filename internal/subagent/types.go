// Package subagent implements nanite's inline subagent spawn flow —
// the control path by which a primary chat agent creates and
// supervises a short-lived secondary agent to handle a subtask
// (dogfood case: primary spawns one secondary to hit Engine MCP for
// a project summary, another to run a knowledge task).
//
// Scope of this package:
//   - Run lifecycle (subagent_runs) persisted across process
//     restarts.
//   - Spawn / Status / Cancel public API.
//   - Runner interface — the actual LLM-backed execution is injected
//     by the container so this package stays independent of the
//     chat engine. A stub runner keeps the integration testable.
//   - Reply delivery: on completion, a message is posted back to
//     the parent via the messaging subsystem (kind=subagent_result,
//     channel=chat for sync, channel=inbox for async).
//
// Approval flow (D13) is DEFERRED to a follow-up commit — this
// package ships the happy path only, per session direction:
//   - Mode "api"  auto-approves (caller is assumed trusted).
//   - Mode "sync" runs immediately (treated like api for MVP).
//   - Mode "async" runs immediately but returns early (result lands
//     in the parent's inbox channel).
//
// Interactive approval (envelope-driven, user accepts/rejects) is
// flagged in the run-lifecycle table (status=requested → approved)
// but no approval envelope is emitted yet. That's T9.2.
package subagent

import "context"

// Mode names the execution style for a spawn request.
const (
	ModeSync        = "sync"
	ModeAsync       = "async"
	ModeAPI         = "api"
	ModeInteractive = "interactive"
)

// Run status constants. Lifecycle:
//
//	requested → approved → running → completed | failed | over_budget | stalled | cancelled
//	requested → rejected
//
// `requested` is the initial state only when interactive approval
// is pending (T9.2 scope). MVP paths skip straight to `running`
// since approval is auto-granted for sync/async/api.
//
// CW-20260519-0074 — run status taxonomy (audit §P3). Before this
// change `execute` had exactly one error branch: any non-nil runErr →
// `failed`. A context.DeadlineExceeded on a *productive* run was
// recorded identically to a genuine crash, so the run status carried
// no diagnostic signal. The terminal error states now split:
//
//   - completed   — produced a usable result (the output-presence GATE
//     is the separate CW-20260519-0067; `completed` here
//     still means only "the runner returned no error").
//   - over_budget — the wall-clock backstop fired but the run was
//     making progress (non-zero tool calls). NOT a
//     failure: it must not burn a retry budget or fire
//     on_fail (mirrors Torque's `canceled`-vs-`failed`
//     split, scheduler.go:668-701 / lifecycle.go:105-112).
//   - stalled     — the provider-stream inactivity watchdog fired
//     (CW-20260517-0036) with no progress: the run went
//     silent.
//   - failed      — reserved for genuine crashes and the
//     fabrication-detector trip.
const (
	StatusRequested  = "requested"
	StatusApproved   = "approved"
	StatusRunning    = "running"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusOverBudget = "over_budget"
	StatusStalled    = "stalled"
	StatusCancelled  = "cancelled"
	StatusRejected   = "rejected"
)

// IsTerminalStatus reports whether s is a terminal run state — one a
// reaper / sweep / dispatch-wait should treat as "the run is done".
// Centralizes the set so a future status addition is a one-line change
// and no consumer silently misses a new terminal value.
func IsTerminalStatus(s string) bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusOverBudget,
		StatusStalled, StatusCancelled, StatusRejected:
		return true
	default:
		return false
	}
}

// On-failure routing policy applied by execute's retry loop
// (CW-20260519-0075, audit §P6). Mirrors Torque's task.OnFail routing in
// internal/runtime/scheduler/lifecycle.go:189-279 (retry / block /
// escalate). At the subagent layer:
//
//   - OnFailRetry    loop in-place on a retriable terminal outcome until
//     retry_count reaches max_retries.
//   - OnFailBlock    terminate immediately on failure; leave the row in
//     the failed/stalled state for the parent task / caller
//     to handle (no further attempts).
//   - OnFailEscalate behaves identically to block at the subagent layer
//     today — escalation is a parent-task lifecycle concern
//     and the inline subagent runner has no escalation
//     surface to invoke. The value is accepted so a parent
//     task / dispatcher can pass through its own OnFail
//     without coercion, and the future escalation hook can
//     branch on this value without a schema change.
//
// over_budget is always a candidate for resume regardless of OnFail
// policy: it is by construction "the run made progress but hit the
// wall-clock backstop" (Torque's canceled vs failed split, see
// scheduler.go:668-701). Stopping a productive run that just hit a clock
// is not a failure to route on. The cap is still max_retries — an
// over_budget resume that itself goes over budget eventually terminates.
const (
	OnFailRetry    = "retry"
	OnFailBlock    = "block"
	OnFailEscalate = "escalate"
)

// DefaultMaxRetries is the cap on retry attempts when SpawnRequest does
// not specify one. Mirrors Torque's default (internal/service/task.go:274
// — orDefaultInt(input.MaxRetries, 3)). retry_count is the number of
// *additional* attempts past the first; a MaxRetries=3 chain runs up to
// 4 times before terminating (one initial + 3 retries).
const DefaultMaxRetries = 3

// DefaultOnFail is the routing policy applied when SpawnRequest does not
// set one. Mirrors Torque's default OnFail (retry) for kind=agent tasks.
const DefaultOnFail = OnFailRetry

// IsValidOnFail reports whether s is a recognized on_fail value. The DB
// CHECK constraint enforces the same set; this helper is the source of
// truth at the Spawn boundary so an invalid request is rejected before
// any DB write rather than failing on INSERT.
func IsValidOnFail(s string) bool {
	switch s {
	case OnFailRetry, OnFailBlock, OnFailEscalate:
		return true
	default:
		return false
	}
}

// IsRetriableStatus reports whether a terminal status represents an
// outcome that may be retried. Used by execute's retry loop to gate the
// decision whether to attempt another iteration:
//
//   - over_budget: yes — by construction the run was productive; resume
//     mode reuses the child session so the next attempt continues the
//     conversation.
//   - stalled:     yes — a stalled provider stream may recover on a
//     fresh stream. Retry restarts the provider call against the same
//     child session (the chat history carries over).
//   - failed:      yes for transient/unknown failures. The fabrication
//     detector trip and approval-rejection are pre-filtered by the
//     classifier in execute (see retryableFailureError).
//
// completed, cancelled, rejected are never retried: completed is a
// success, the other two are operator decisions that must not be
// overridden by the retry loop.
func IsRetriableStatus(s string) bool {
	switch s {
	case StatusOverBudget, StatusStalled, StatusFailed:
		return true
	default:
		return false
	}
}

// SpawnRequest is the caller-supplied input for a spawn.
type SpawnRequest struct {
	// ParentSessionID is the session the spawning agent is in. The
	// subagent's reply message is delivered here.
	ParentSessionID string
	// ParentAgentID is the agent that requested the spawn — the
	// "from" on the reply message when the subagent completes.
	// Conventionally the primary agent's id.
	ParentAgentID string
	// Role is the role slug to boot the subagent with (e.g.
	// "file-frontend" for a file-based role). The runner resolves
	// this against the agent registry.
	Role string
	// Prompt is the initial prompt the subagent processes.
	Prompt string
	// Mode controls execution + result-delivery semantics. See Mode
	// constants.
	Mode string
	// InputsJSON carries caller-specified inputs as a JSON blob
	// (opaque to the subagent subsystem; passed to the runner).
	InputsJSON string
	// TimeoutSeconds caps the runner's wall time. Zero means use
	// the default (300).
	TimeoutSeconds int
	// Provider overrides the child agent profile's DefaultProvider
	// when non-empty. Empty means "use agent profile default".
	// Enables budget-aware routing (e.g. pty-claude for heavy tasks,
	// anthropic for lightweight ones) without changing the agent profile.
	Provider string
	// WorkspaceID is the workspace the spawn is scoped to. Required
	// for trust resolution (H1, CW-20260421-0014). Empty string
	// causes the trust gate to fall back to TrustNormal.
	WorkspaceID string
	// AgentProfileID is the DB ID of the agent profile being spawned.
	// Required for trust resolution. Empty string falls back to TrustNormal.
	AgentProfileID string

	// MaxRetries caps the number of *additional* attempts past the first
	// when the run terminates in a retriable state (CW-20260519-0075, audit
	// §P6). Zero means "use DefaultMaxRetries". A negative value is
	// normalized to zero (no retries — single-shot). Mirrors Torque's
	// MaxRetries default tier (internal/service/task.go:274).
	MaxRetries int

	// OnFail is the routing policy applied to a genuine failure outcome.
	// Empty string means "use DefaultOnFail" (retry). See OnFail* constants
	// for the recognized values. An unrecognized value is rejected by
	// Spawn before any DB write.
	OnFail string
}

// Run represents a single spawn lifecycle row persisted in
// subagent_runs.
type Run struct {
	ID              string `json:"id"`
	ParentSessionID string `json:"parent_session_id"`
	ChildSessionID  string `json:"child_session_id"`
	Role            string `json:"role"`
	Prompt          string `json:"prompt"`
	Mode            string `json:"mode"`
	Status          string `json:"status"`
	InputsJSON      string `json:"inputs_json"`
	ResultJSON      string `json:"result_json"`
	Error           string `json:"error"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	CreatedAt       string `json:"created_at"`
	StartedAt       string `json:"started_at"`
	CompletedAt     string `json:"completed_at"`

	// G-4 additions.
	ParentAgentID      string `json:"parent_agent_id"`
	EnvelopeInstanceID string `json:"envelope_instance_id"`
	ApprovedAt         string `json:"approved_at"`
	ApprovedBy         string `json:"approved_by"`
	RejectedAt         string `json:"rejected_at"`
	RejectionReason    string `json:"rejection_reason"`

	// Phase B — per-spawn provider override.
	// Empty string means the child session used the agent profile's
	// DefaultProvider. Non-empty is the override that was applied.
	Provider string `json:"provider,omitempty"`

	// CW-20260519-0075 (audit §P6) — checkpoint/resume + retry lifecycle.

	// RetryCount is the number of *additional* attempts past the first.
	// 0 = "the row reflects a single attempt, no retry happened".
	// Bumped by execute's loop when a retriable outcome triggers another
	// iteration; capped at MaxRetries.
	RetryCount int `json:"retry_count"`

	// MaxRetries is the per-run cap on retries (Torque parity). When
	// RetryCount reaches MaxRetries the chain terminates regardless of
	// OnFail policy.
	MaxRetries int `json:"max_retries"`

	// OnFail is the routing policy on a genuine failure outcome
	// (`retry` / `block` / `escalate`). Mirrors Torque's task.OnFail. See
	// the OnFail* constants for semantics.
	OnFail string `json:"on_fail"`

	// AttemptsJSON is a JSON array recording per-attempt audit detail for
	// every iteration EXCEPT the final one (whose status/error/result_json
	// live in the top-level columns). Each entry is a
	// `{"status","error","result_json","started_at","completed_at"}`
	// object — same shape as the run row's terminal fields. Empty array
	// (the default) means "this row reflects the only attempt".
	AttemptsJSON string `json:"attempts_json"`
}

// Result is the output a Runner returns on successful completion.
// Carries a short summary for the reply message body plus a
// free-form JSON blob for richer structured results.
type Result struct {
	Summary    string
	ResultJSON string
}

// SubagentStreamSink receives subagent status transitions for delivery
// to the parent session's SSE stream. Wired by the container; nil is
// permitted (emission is a no-op when no sink is set).
type SubagentStreamSink interface {
	SubagentStatusChanged(parentSessionID string, payloadJSON []byte)
}

// CompletionReactor is notified after a subagent run's completion
// message has been posted to the parent session's inbox
// (CW-20260520-0001, Layer 2 — "harness reacts"). Implemented by
// internal/service so it can consult ChatService's in-flight-generation
// registry and, per the parent session's configured policy, proactively
// trigger a harness-initiated turn. Defined here (not in internal/service)
// to avoid an import cycle: internal/service constructs subagent.Service,
// not the reverse. Wired via SetCompletionReactor; nil is permitted
// (reaction is a no-op when no reactor is set).
type CompletionReactor interface {
	ReactToCompletion(ctx context.Context, run *Run, messageID string)
}
