package recovery

import (
	"context"

	"github.com/hollis-labs/nanite/internal/runtime/agent"
)

// Dependencies is the broker's composition-root injection point. The
// chat service constructs one and passes it into NewBroker.
//
// Each interface is intentionally narrow — the broker doesn't need (and
// must not depend on) the full surface of the upstream component.
type Dependencies struct {
	// AgentBoot dispatches replacement sessions. Production wiring is
	// the agent.Boot package function bound through a thin shim; tests
	// inject scripted fakes.
	AgentBoot AgentBoot

	// BootDir performs sandbox-dir-level remediations. Repopulate
	// rewrites the full per-session sandbox; RegenerateCLAUDEMD touches
	// only the CLAUDE.md slot. Idempotent and bounded.
	BootDir BootDirOps

	// MCP performs MCP-transport-level remediations. RestartTransport
	// tears down + re-spawns the per-session MCP subprocess. Idempotent.
	MCP MCPControl

	// Credentials refreshes API credentials for an agent profile. Used
	// when stderr indicates auth failure (401/403/unauthorized).
	Credentials CredentialOps

	// Store transitions the runtime row state on retry and persists
	// breadcrumbs. Narrower than agent.RuntimeStore — the broker only
	// needs the relaunch + breadcrumb hooks.
	Store BrokerStore

	// Envelope sinks user-facing recovery messages. Production wiring
	// targets the chat SSE pipeline; tests collect into a slice for
	// assertion.
	Envelope EnvelopeSink

	// HTTPRetry dispatches a bootdir-free retry for an HTTP-provider
	// (anthropic/openai/... — any provider with no implemented bootdir
	// Layout, per agent.HasBootdirLayout) chat session. DispatchRetry
	// consults agent.HasBootdirLayout(ev.Provider) to choose this path
	// over AgentBoot: HTTPRetry never touches AgentBoot.Boot, so the
	// CW-20260815-0024 guarantee ("never dispatch a doomed CLI-boot
	// retry for a no-bootdir-layout provider") holds structurally
	// regardless of caller.
	//
	// Unlike AgentBoot, production wiring for this field is NOT supplied
	// at NewBroker time — the concrete adapter closes over the chat
	// service's RetryLastMessage, and chatServiceImpl is constructed
	// AFTER BuildAgentDependencies (which constructs the broker). The
	// composition root installs it post-construction via
	// Broker.SetHTTPRetry, mirroring SetReplacementSessionHook's
	// identical construction-order workaround. Tests may still set it
	// directly in a Dependencies literal passed to NewBroker.
	HTTPRetry HTTPRetry

	// Logger is optional. Nil-safe — methods log via slog.Default()
	// when this is unset.
	Logger Logger
}

// AgentBoot is the broker's hook into agent.Boot for replacement-session
// dispatch. Production wiring is `recovery.NewAgentBootFunc(agentDeps)`
// which closes over a *agent.Dependencies and forwards to agent.Boot.
type AgentBoot interface {
	// Boot dispatches a replacement session. The broker calls this with
	// the same SessionID as the failed session — the lifecycle layer is
	// responsible for transitioning the runtime row from failed ->
	// launching before this call, via Store.MarkRuntimeRelaunching.
	Boot(ctx context.Context, opts agent.Options) (*agent.Session, error)
}

// HTTPRetry is the broker's hook for retrying an HTTP-provider chat turn
// directly — no bootdir/agent.Boot involved. Production wiring
// (internal/service.recoveryHTTPRetryAdapter) re-invokes the chat
// harness's existing "retry last message" path (the same mechanism a
// user's manual [Retry] click uses: reset the circuit breaker, find the
// last user message, dispatch a fresh generateResponse turn through the
// full harness) against the failed session. Deliberately narrow — like
// AgentBoot, it exposes only what the broker needs (dispatch a retry for
// a session), not the chat service's full surface.
type HTTPRetry interface {
	// Retry dispatches a fresh attempt at ev.SessionID's last turn by
	// calling the resolved HTTP provider directly (no subprocess, no
	// boot dir). Mirrors AgentBoot.Boot's "dispatch, don't await
	// steady-state" contract: a nil error means the retry was launched
	// successfully — the turn's own success/failure surfaces later via
	// the session's SSE stream and, on a further failure, a fresh
	// OnSessionExit observation (which is what drives the next backoff
	// attempt, exactly as a CLI replacement session's own crash would
	// re-enter OnSessionExit).
	Retry(ctx context.Context, ev *FailureEvent) error
}

// BootDirOps is the sandbox-directory remediation contract.
type BootDirOps interface {
	// Repopulate rewrites the full per-session sandbox directory
	// (CLAUDE.md, agent-context.md, envelope-schema.md, .mcp.json).
	// Idempotent. Must complete within a bounded timeout (10s default
	// per remediator).
	Repopulate(ctx context.Context, sessionID string) error

	// RegenerateCLAUDEMD rewrites just the CLAUDE.md slot for the
	// session. Used when the agent appears stuck on a stale prompt
	// (watchdog_kill cause).
	RegenerateCLAUDEMD(ctx context.Context, sessionID string) error
}

// MCPControl is the MCP-transport remediation contract.
type MCPControl interface {
	// RestartTransport tears down and re-spawns the MCP subprocess for
	// the session. Idempotent. Bounded by the remediator timeout.
	RestartTransport(ctx context.Context, sessionID string) error
}

// CredentialOps is the credential-refresh remediation contract.
type CredentialOps interface {
	// Refresh re-resolves API credentials for the named agent profile.
	// Bounded by the remediator timeout.
	Refresh(ctx context.Context, agentProfile string) error
}

// BrokerStore is the persistence contract the broker needs from the
// runtime store. Narrower than agent.RuntimeStore.
type BrokerStore interface {
	// MarkRuntimeRelaunching transitions the runtime row from
	// state="failed" to state="launching" without creating a new row.
	// Required because agent.Boot's CreateRuntimeRow would conflict on
	// the unique sessionID key.
	MarkRuntimeRelaunching(sessionID, reason string) error

	// WriteBreadcrumb persists a postmortem record. The implementation
	// inserts into nanite_recovery_breadcrumbs (Phase 8 migration).
	WriteBreadcrumb(b Breadcrumb) error
}

// EnvelopeSink delivers user-facing recovery envelopes. The production
// implementation forwards into the chat SSE pipeline using the existing
// envelope kinds (info-card / error-report / chat-loop-terminated).
type EnvelopeSink interface {
	// Emit delivers an envelope to the chat session's SSE channel.
	// Renderer-agnostic — the broker constructs the envelope payload
	// and the sink only forwards.
	Emit(sessionID string, env Envelope) error
}

// Envelope is the broker's user-facing message payload. Maps onto the
// existing envelope schemas (info-card / error-report /
// chat-loop-terminated) — the kind field selects which schema to render.
type Envelope struct {
	// Kind is one of "info-card", "error-report", "chat-loop-terminated".
	// No new envelope kinds — the broker reuses what the chat harness
	// already renders.
	Kind string

	// Title is the envelope title shown in the FE notification surface.
	Title string

	// Content is the envelope body (markdown-permitted).
	Content string

	// CancelToken, when non-empty, surfaces a [Cancel retry] button on
	// the info-card. The FE posts cancel_retry events back to the
	// broker carrying this token. Empty for non-cancellable envelopes
	// (e.g. error-report).
	CancelToken string

	// Severity is the envelope severity level ("info" / "warning" /
	// "error"). Drives FE styling.
	Severity string
}

// Logger is the broker's logging seam. Optional; nil-safe via the
// internal default that delegates to slog.Default().
type Logger interface {
	// Info logs a non-error event with key/value attributes.
	Info(msg string, kv ...any)

	// Warn logs a recoverable issue.
	Warn(msg string, kv ...any)

	// Error logs an unrecoverable issue.
	Error(msg string, kv ...any)
}

// classifierState captures the per-session classifier state that drives
// the "transient retry on first occurrence; permanent on second" rule
// AND the broker-level hard-cap attempt counter. Held inside the
// broker; not exposed.
type classifierState struct {
	// attemptCount is the broker-level attempt counter (1-indexed).
	// Increments on every OnSessionExit invocation for this sessionID.
	// Drives the broker hard cap (Attempt > maxRetries -> Permanent)
	// and feeds FailureEvent.Attempt for the classifier's own
	// "first vs second occurrence" rule.
	attemptCount int

	// transientCountByCause counts how many transient retries the
	// broker has already issued for this session keyed by
	// ExitError.Cause. Reserved for cause-aware second-occurrence
	// escalation; v1 uses the simpler attemptCount rule.
	transientCountByCause map[string]int
}

// errNoAgentBootWired surfaces when the broker is asked to dispatch a
// replacement session but no AgentBoot was provided in Dependencies.
// Treated as a permanent failure.
var errNoAgentBootWired = brokerErr("recovery: AgentBoot not wired in Dependencies")

// errNoHTTPRetryWired surfaces when DispatchRetry routes a no-bootdir-
// layout session to the HTTP retry path but no HTTPRetry was provided
// (Dependencies literal in a test, or SetHTTPRetry never called in
// production — e.g. a degraded boot). Treated as a permanent failure,
// same as errNoAgentBootWired for the CLI path.
var errNoHTTPRetryWired = brokerErr("recovery: HTTPRetry not wired in Dependencies")

// brokerErr is a tiny error type used for sentinel errors inside the
// recovery package. Avoids pulling in errors.New / fmt for the few
// constants we need.
type brokerErr string

func (e brokerErr) Error() string { return string(e) }
