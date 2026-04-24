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
//     the parent via the messaging subsystem (kind=reply,
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

// Mode names the execution style for a spawn request.
const (
	ModeSync        = "sync"
	ModeAsync       = "async"
	ModeAPI         = "api"
	ModeInteractive = "interactive"
)

// Run status constants. Lifecycle:
//
//	requested → approved → running → completed | failed | cancelled
//	requested → rejected
//
// `requested` is the initial state only when interactive approval
// is pending (T9.2 scope). MVP paths skip straight to `running`
// since approval is auto-granted for sync/async/api.
const (
	StatusRequested = "requested"
	StatusApproved  = "approved"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusRejected  = "rejected"
)

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
