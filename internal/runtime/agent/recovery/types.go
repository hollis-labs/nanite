package recovery

import (
	"time"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// FailureEvent is the broker's input. Aggregates the lib-side ExitError
// with nanite-side context (sandbox state, MCP transport health, path
// grants, lineage). Constructed by the chat composition root's Wait
// observer; passed into broker.OnSessionExit.
type FailureEvent struct {
	// SessionID is the chat-harness sessionID. Stays stable across the
	// failure -> remediation -> replacement-session cycle.
	SessionID string

	// AgentProfile identifies the agent profile slug that was running.
	AgentProfile string

	// Provider is the resolved provider name (claude / codex / opencode).
	Provider string

	// Mode is the lifecycle policy the failed session was running under.
	// Stored as the agent.Mode string form to avoid a recovery -> agent
	// -> recovery cycle if Mode ever needs richer typing.
	Mode string

	// Attempt is the broker-level attempt counter (1-indexed). Distinct
	// from agentsessions.SupervisorOptions.RestartOnCrash's lib-level
	// attempt count.
	Attempt int

	// Exit is the structured terminal exit info from go-agent-sessions.
	// Non-nil for OnSessionExit; may be nil for OnRestart prevExit when
	// the lib didn't capture structured data.
	Exit *agentsessions.ExitError

	// LastTurnPos is the last stream-json line the harness consumed
	// before the failure. Used by the replacement session's kickoff for
	// "preserved N steps" messaging when events.Done payload bridging
	// lands.
	LastTurnPos int

	// StderrTail is the last N KB of stderr captured for the failed
	// process. Fed into the classifier's stderr-substring rules
	// (401/403/unauthorized -> RefreshCredentials, etc.).
	StderrTail string

	// SandboxDirState describes the per-session sandbox directory at the
	// moment of failure (populated/missing/partial; which key files
	// present). Drives RepopulateSandbox decisions.
	SandboxDirState SandboxState

	// MCPTransport describes MCP-side health at the moment of failure.
	// Drives RefreshMCPTransport decisions.
	MCPTransport MCPState

	// PathGrants are the cwd-relevant grant entries the failed session
	// held. Replacement session inherits these.
	PathGrants []string

	// LineageOf is the parent session ID when this is a subagent.
	// Replacement preserves this.
	LineageOf string

	// Workdir is the absolute project directory the failed session was
	// bound to. Replacement reuses it.
	Workdir string

	// OccurredAt is the wall-clock timestamp of the failure observation.
	OccurredAt time.Time

	// SessionAge is how long the session was alive before failure.
	// Drives signal-9 + short-age = ConfigPermissions classification
	// (likely missing binary) vs signal-9 + healthy-age = Transient.
	SessionAge time.Duration
}

// SandboxState snapshots the per-session sandbox directory at the moment
// of failure. The classifier branches on the populated/missing axis;
// remediation needs the directory path to repopulate.
type SandboxState struct {
	// Path is the absolute sandbox directory path.
	Path string

	// Missing is true when the directory or its key files are absent.
	// Drives the SandboxDirState.Missing rule in Classify.
	Missing bool

	// PresentFiles lists the relative paths that ARE present. Useful for
	// debugging/postmortem; not consulted by the classifier itself.
	PresentFiles []string
}

// MCPState snapshots MCP transport health at the moment of failure.
type MCPState struct {
	// Down is true when the MCP transport is unreachable (recent ping
	// failure, broken pipe on stdin). Drives the MCPTransport.Down rule.
	Down bool

	// LastOK is the last time the transport was confirmed healthy.
	LastOK time.Time

	// Reason carries the underlying error string when Down is true.
	Reason string
}

// Class is the broker's top-level failure classification. Three values;
// each maps to a documented action.
type Class int

const (
	// ClassUnknown is the zero value — surfaces unset classifier output
	// as a programming error rather than silently treating it as
	// Transient. Never persisted.
	ClassUnknown Class = iota

	// ClassTransient: retry without intervention. Single-attempt retries
	// for SIGSEGV, SIGKILL with healthy session age, idle-timeout, etc.
	// Two transient retries on the same failure -> Permanent (rule lives
	// in Classify).
	ClassTransient

	// ClassConfigPermissions: a remediable configuration issue. Run the
	// associated Remediation action, then retry. Examples: sandbox dir
	// missing, MCP transport down, expired credentials, stale CLAUDE.md
	// after agent profile change.
	ClassConfigPermissions

	// ClassPermanent: not recoverable by the broker. Surface error-report
	// or chat-loop-terminated to the user. Examples: exit code 127
	// (binary missing), restart_exhausted, broker hard-cap reached.
	ClassPermanent
)

// String renders Class for telemetry/log output.
func (c Class) String() string {
	switch c {
	case ClassTransient:
		return "transient"
	case ClassConfigPermissions:
		return "config_permissions"
	case ClassPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// Remediation enumerates the broker's repair actions. RemediationNone
// applies to ClassTransient (no remediation; just retry) and to
// ClassPermanent (no remediation; escalate). Non-None values apply to
// ClassConfigPermissions.
type Remediation int

const (
	// RemediationNone is the zero value — no automated repair. Used for
	// ClassTransient (retry as-is) and ClassPermanent (no retry).
	RemediationNone Remediation = iota

	// RemediationRepopulateSandbox rewrites the per-session sandbox dir
	// (CLAUDE.md, agent-context.md, .mcp.json, envelope-schema.md).
	// Triggered when SandboxDirState.Missing is true.
	RemediationRepopulateSandbox

	// RemediationRefreshMCPTransport restarts the MCP subprocess
	// transport for the session. Triggered when MCPTransport.Down is true.
	RemediationRefreshMCPTransport

	// RemediationRefreshCredentials re-resolves API credentials for the
	// agent profile. Triggered when stderr contains 401/403/unauthorized.
	RemediationRefreshCredentials

	// RemediationRegenerateCLAUDEMD rewrites just the CLAUDE.md slot
	// content (agent profile + mode + rules) without touching the rest
	// of the sandbox. Triggered when watchdog_kill suggests a stuck
	// agent that needs a fresh prompt.
	RemediationRegenerateCLAUDEMD
)

// String renders Remediation for telemetry/log output.
func (r Remediation) String() string {
	switch r {
	case RemediationRepopulateSandbox:
		return "repopulate_sandbox"
	case RemediationRefreshMCPTransport:
		return "refresh_mcp_transport"
	case RemediationRefreshCredentials:
		return "refresh_credentials"
	case RemediationRegenerateCLAUDEMD:
		return "regenerate_claudemd"
	default:
		return "none"
	}
}

// Description renders Remediation as a short user-facing phrase that
// slots into the "Resolved {{X}}; retrying…" template on info-card
// envelopes for ActionRetryConfigFixed. Distinct from String() —
// String is for telemetry/logs (machine-friendly), Description is for
// chat surface (human-friendly).
func (r Remediation) Description() string {
	switch r {
	case RemediationRepopulateSandbox:
		return "rebuilt the agent's sandbox directory"
	case RemediationRefreshMCPTransport:
		return "restarted the agent's MCP transport"
	case RemediationRefreshCredentials:
		return "refreshed agent credentials"
	case RemediationRegenerateCLAUDEMD:
		return "refreshed the agent's instructions"
	default:
		return "a configuration issue"
	}
}

// Classification is the classifier's decision. Reason is human-readable
// and threads into both telemetry breadcrumbs and (when ClassPermanent)
// the user-facing error-report payload.
type Classification struct {
	Class       Class
	Reason      string
	Remediation Remediation
}

// Action is the user-facing action the broker takes on a FailureEvent.
// Drives envelope rendering — see envelope.go.
type Action int

const (
	// ActionUnknown is the zero value — surfaces unset action as a
	// programming error.
	ActionUnknown Action = iota

	// ActionRetryTransient: emit info-card "Reconnecting agent" and
	// dispatch a replacement session. No remediation in between.
	ActionRetryTransient

	// ActionRetryConfigFixed: emit info-card "Fixed an agent issue" with
	// remediation detail, dispatch a replacement session.
	ActionRetryConfigFixed

	// ActionPermanentFailure: emit error-report (or chat-loop-terminated
	// for runaway-shaped failures) and stop. No replacement.
	ActionPermanentFailure
)

// String renders Action for telemetry/log output.
func (a Action) String() string {
	switch a {
	case ActionRetryTransient:
		return "retry_transient"
	case ActionRetryConfigFixed:
		return "retry_config_fixed"
	case ActionPermanentFailure:
		return "permanent_failure"
	default:
		return "unknown"
	}
}

// Outcome closes the loop on a broker-handled FailureEvent. Persisted on
// the breadcrumb; powers postmortem queries ("which classes recovered,
// which escalated").
type Outcome int

const (
	// OutcomeUnknown is the zero value.
	OutcomeUnknown Outcome = iota

	// OutcomeRemediated: ClassConfigPermissions remediation succeeded
	// AND the replacement session reached steady-state.
	OutcomeRemediated

	// OutcomeTransientRetrySucceeded: ClassTransient retry produced a
	// healthy replacement session.
	OutcomeTransientRetrySucceeded

	// OutcomePermanent: failure escalated to the user; no further retry.
	OutcomePermanent

	// OutcomeCancelled: user clicked the [Cancel retry] info-card button
	// before the retry completed; original failure escalated as
	// permanent.
	OutcomeCancelled
)

// String renders Outcome for telemetry/log output.
func (o Outcome) String() string {
	switch o {
	case OutcomeRemediated:
		return "remediated"
	case OutcomeTransientRetrySucceeded:
		return "transient_retry_succeeded"
	case OutcomePermanent:
		return "permanent"
	case OutcomeCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Breadcrumb is the postmortem record persisted on every broker-handled
// FailureEvent. Schema mirrors the nanite_recovery_breadcrumbs migration
// added in Phase 8.
type Breadcrumb struct {
	Timestamp           time.Time
	SessionID           string
	Class               Class
	Cause               string // ExitError.Cause verbatim, empty for non-supervised exits
	Remediation         Remediation
	Action              Action
	Outcome             Outcome
	AttemptCount        int
	DurationFromFailure time.Duration
	// Reason is the classifier's human-readable reason; preserved here
	// for postmortem queries that aggregate by reason without rejoining.
	Reason string
}

// MaxBrokerRetries is the broker-level hard cap. Combined with the
// lib-level RestartOnCrash=2, the total spawn budget per session is up
// to 5 attempts before a Permanent escalation.
//
// Configurable via Broker.WithMaxRetries; this is the default.
const MaxBrokerRetries = 3
