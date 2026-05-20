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
//                   is the separate CW-20260519-0067; `completed` here
//                   still means only "the runner returned no error").
//   - over_budget — the wall-clock backstop fired but the run was
//                   making progress (non-zero tool calls). NOT a
//                   failure: it must not burn a retry budget or fire
//                   on_fail (mirrors Torque's `canceled`-vs-`failed`
//                   split, scheduler.go:668-701 / lifecycle.go:105-112).
//   - stalled     — the provider-stream inactivity watchdog fired
//                   (CW-20260517-0036) with no progress: the run went
//                   silent.
//   - failed      — reserved for genuine crashes and the
//                   fabrication-detector trip.
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
