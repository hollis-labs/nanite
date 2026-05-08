package recovery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/nanite/internal/runtime/agent"
)

// Broker is the in-process subagent recovery broker. Construct with
// NewBroker; satisfies the agent.RecoveryHooks contract once that
// interface lands in Phase 7.
//
// Concurrency: all public methods are safe for concurrent callers.
// Per-session classifier state is held under broker.mu; the broker
// serializes failure handling per-session via that lock to prevent two
// concurrent OnSessionExit observations from racing on the same session.
type Broker struct {
	deps Dependencies

	maxRetries int

	// remediationTimeout bounds each remediation action. 10s default;
	// configurable via WithRemediationTimeout.
	remediationTimeout time.Duration

	mu sync.Mutex

	// stateBySession holds the per-session classifier scratch (transient
	// counts by cause, etc.). Cleared when a replacement session reaches
	// steady-state or escalates to permanent.
	stateBySession map[string]*classifierState

	// activeRetries tracks in-flight retry tokens so cancel_retry events
	// from the FE can abort. Map key is the cancel token; value is the
	// CancelFunc bound to the retry's context.
	activeRetries map[string]context.CancelFunc
}

// NewBroker constructs a Broker with the supplied dependencies and
// sensible defaults (MaxBrokerRetries, 10s remediation timeout, slog
// default logger). Override defaults via With* options.
func NewBroker(deps Dependencies, opts ...Option) *Broker {
	if deps.AgentBoot == nil {
		// Production wiring always supplies AgentBoot; this guard exists
		// for tests + early integrations that want classify-only behavior.
		// DispatchRetry returns errNoAgentBootWired when reached.
	}
	b := &Broker{
		deps:               deps,
		maxRetries:         MaxBrokerRetries,
		remediationTimeout: 10 * time.Second,
		stateBySession:     make(map[string]*classifierState),
		activeRetries:      make(map[string]context.CancelFunc),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Option configures a Broker at construction time.
type Option func(*Broker)

// WithMaxRetries overrides the broker-level hard cap. Default is
// MaxBrokerRetries (3). Combined with go-agent-sessions's RestartOnCrash=2
// the total spawn budget is up to maxRetries + 1 + 2 = 6 attempts.
func WithMaxRetries(n int) Option {
	return func(b *Broker) {
		if n < 0 {
			n = 0
		}
		b.maxRetries = n
	}
}

// WithRemediationTimeout overrides the per-remediation bounded timeout.
// Default is 10s.
func WithRemediationTimeout(d time.Duration) Option {
	return func(b *Broker) {
		if d > 0 {
			b.remediationTimeout = d
		}
	}
}

// OnRestart implements the agent.RecoveryHooks contract. Invoked from
// agent.Boot's existing SupervisorOptions.OnRestart closure when the
// supervisor itself triggered a restart. The broker observes (records
// breadcrumb, optionally overlays an info-card) but does not re-dispatch;
// the lib has already handled the restart.
//
// attempt is 1-indexed (the second spawn fires OnRestart with attempt=1).
// prevExit may be non-nil with structured cause info or nil if the lib
// didn't capture it.
func (b *Broker) OnRestart(sessionID string, attempt int, prevExit *agentsessions.ExitError) {
	logger := b.logger()
	cause := ""
	if prevExit != nil {
		cause = prevExit.Cause
	}
	logger.Info("recovery: lib-level restart observed",
		"session_id", sessionID,
		"attempt", attempt,
		"cause", cause)

	// Phase 8: write breadcrumb here so postmortem queries see lib-level
	// restart attempts even when they succeed and never escalate to the
	// broker's OnSessionExit path.
	b.writeBreadcrumb(Breadcrumb{
		Timestamp:   time.Now(),
		SessionID:   sessionID,
		Class:       ClassTransient,
		Cause:       cause,
		Remediation: RemediationNone,
		Action:      ActionRetryTransient,
		Outcome:     OutcomeUnknown, // unknown at observation time; the next OnSessionExit (if any) updates
		AttemptCount: attempt,
		Reason:       "lib-level restart attempt",
	})

	// Phase 5 will optionally overlay an info-card here. v1 stays quiet
	// during lib-level restarts to avoid noise — the chat surface only
	// learns about recovery on terminal exit (broker's OnSessionExit).
}

// OnSessionExit implements the agent.RecoveryHooks contract. Invoked
// from the chat composition root's Wait-observer goroutine when the
// session's terminal *agentsessions.ExitError lands.
//
// meta is a free-form context bag the chat harness populates with
// session-level state (StderrTail, SandboxDirState, MCPTransport,
// LastTurnPos, etc.) — keys documented in package docs.
//
// This is the broker's main entry point. It classifies the failure,
// runs any remediation, dispatches a replacement session if classified
// recoverable, and emits user-facing envelopes through Dependencies.Envelope.
//
// Phase 1: signature only; Phase 6 wires the orchestration.
func (b *Broker) OnSessionExit(sessionID string, exit *agentsessions.ExitError, meta map[string]any) {
	logger := b.logger()
	if exit == nil {
		// Clean exit — nothing to recover. Defensive: callers should
		// not invoke OnSessionExit on nil exits, but we no-op rather
		// than crash if they do.
		logger.Info("recovery: nil exit skipped", "session_id", sessionID)
		return
	}

	// Phase 6 will own the orchestration:
	//   1. Construct FailureEvent from (sessionID, exit, meta) + per-session state.
	//   2. Classify.
	//   3. If ClassConfigPermissions, run Remediate.
	//   4. If retryable and not over the broker hard cap, DispatchRetry.
	//   5. Render the user-facing envelope and emit through Dependencies.Envelope.
	//   6. Write breadcrumb with the resolved Outcome.
	//
	// For Phase 1, observe-and-log only so the package compiles and
	// the agent-side Dependencies.Recovery wiring (Phase 7) has a
	// stable callee.
	logger.Info("recovery: terminal exit observed",
		"session_id", sessionID,
		"cause", exit.Cause,
		"code", exit.Code,
		"signal", exit.Signal,
		"killed", exit.Killed)
}

// CancelRetry aborts an in-flight retry identified by token. Called
// when the FE posts a cancel_retry event (the user clicked the
// [Cancel retry] button on an info-card). Phase 5 wires the FE side;
// Phase 6 wires the broker side. Phase 1 supplies the entry point and
// the cancel-token registry.
func (b *Broker) CancelRetry(token string) {
	if token == "" {
		return
	}
	b.mu.Lock()
	cancel, ok := b.activeRetries[token]
	if ok {
		delete(b.activeRetries, token)
	}
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// stateFor returns the per-session classifier state, lazily creating
// it. Caller must hold b.mu.
func (b *Broker) stateForLocked(sessionID string) *classifierState {
	st, ok := b.stateBySession[sessionID]
	if !ok {
		st = &classifierState{
			transientCountByCause: make(map[string]int),
		}
		b.stateBySession[sessionID] = st
	}
	return st
}

// clearState removes the per-session classifier state. Called when a
// replacement session reaches steady-state OR when the broker escalates
// to permanent.
func (b *Broker) clearState(sessionID string) {
	b.mu.Lock()
	delete(b.stateBySession, sessionID)
	b.mu.Unlock()
}

// writeBreadcrumb persists a breadcrumb via Dependencies.Store. Errors
// from the store are logged but never escalated — telemetry must not
// crash the recovery flow.
func (b *Broker) writeBreadcrumb(c Breadcrumb) {
	if b.deps.Store == nil {
		return
	}
	if err := b.deps.Store.WriteBreadcrumb(c); err != nil {
		b.logger().Warn("recovery: breadcrumb write failed",
			"session_id", c.SessionID,
			"err", err)
	}
}

// logger returns a non-nil Logger — the configured one or a default
// slog wrapper. Avoids nil-checks in every callsite.
func (b *Broker) logger() Logger {
	if b.deps.Logger != nil {
		return b.deps.Logger
	}
	return slogLogger{}
}

// slogLogger adapts slog.Default to the Logger interface for the
// nil-Dependencies.Logger case.
type slogLogger struct{}

func (slogLogger) Info(msg string, kv ...any)  { slog.Info(msg, kv...) }
func (slogLogger) Warn(msg string, kv ...any)  { slog.Warn(msg, kv...) }
func (slogLogger) Error(msg string, kv ...any) { slog.Error(msg, kv...) }

// dispatchRetry is the broker-internal replacement-session dispatch.
// Called from the Phase 6 orchestration once Classify + (optional)
// Remediate decide a retry is appropriate. Reuses the SessionID and
// preserves lineage.
//
// Phase 1: signature + skeleton. Phase 4 fleshes out the Store
// transition + Boot call.
func (b *Broker) dispatchRetry(ctx context.Context, ev *FailureEvent) (*agent.Session, error) {
	if b.deps.AgentBoot == nil {
		return nil, errNoAgentBootWired
	}
	if b.deps.Store != nil {
		if err := b.deps.Store.MarkRuntimeRelaunching(ev.SessionID, "broker retry"); err != nil {
			return nil, err
		}
	}
	parentPtr := ""
	if ev.LineageOf != "" {
		parentPtr = ev.LineageOf
	}
	return b.deps.AgentBoot.Boot(ctx, agent.Options{
		Mode:            agent.ModeLongLived, // Phase 4: derive from ev.Mode
		SessionID:       ev.SessionID,
		AgentProfile:    ev.AgentProfile,
		ParentSessionID: parentPtr,
		Workdir:         ev.Workdir,
	})
}
