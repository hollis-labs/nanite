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
	// from the FE can abort. Map key is the cancel token; value pairs
	// the bound CancelFunc with the sessionID the token was issued for
	// so cross-session token replay (FE bug or stale token from another
	// session) cannot cancel a retry that doesn't belong to it.
	activeRetries map[string]activeRetryEntry

	// replacementHook, when non-nil, is invoked synchronously after a
	// successful DispatchRetry with the replacement session. The chat
	// composition root sets this so it can adopt the replacement into
	// its activeSessions map (otherwise the new session is orphaned and
	// the next user turn boots yet another). nil-safe.
	replacementHook func(sessionID string, sess *agent.Session)
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
		activeRetries:      make(map[string]activeRetryEntry),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Option configures a Broker at construction time.
type Option func(*Broker)

// WithMaxRetries overrides the broker-level hard cap on terminal-exit
// observations the broker will retry for a single chat session. Default
// is MaxBrokerRetries (3): the broker dispatches at most 3 replacement
// sessions (each a fresh agent.Boot) before escalating to ClassPermanent
// on attempt 4 (see orchestration.go's `attempt > b.maxRetries` guard).
//
// The broker cap is independent of the lib-level go-agent-sessions
// RestartOnCrash budget: each broker-dispatched replacement session has
// its own internal RestartOnCrash window (currently 2) that the lib
// consumes silently before surfacing a terminal ExitError back to the
// broker. So the broker's ceiling counts terminal exits the chat layer
// observes, not raw process spawns.
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

// WithReplacementSessionHook installs a callback invoked synchronously
// from the orchestration loop after each successful DispatchRetry. The
// chat composition root uses this to adopt the replacement session into
// its activeSessions map so the next user turn binds to the freshly
// booted process instead of orphaning it and booting another.
//
// Hook semantics:
//   - sessionID is the chat sessionID (stable across the failure → retry
//     cycle); sess is the broker-dispatched replacement.
//   - Invoked before recordOutcome writes the breadcrumb, so any panic
//     in the hook (defended elsewhere with safego) does not corrupt
//     the postmortem record.
//   - Replacement Store should precede activeSessions.Delete on the
//     observer side; see chat_boot_drive.go's observeSessionForRecovery
//     for the call ordering.
func WithReplacementSessionHook(hook func(sessionID string, sess *agent.Session)) Option {
	return func(b *Broker) {
		b.replacementHook = hook
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

// OnSessionExit lives in orchestration.go (Phase 6 implementation).
// Kept here only as a forward-reference comment so readers of broker.go
// see the full hook surface in one place.

// SetReplacementSessionHook installs the replacement-session callback
// post-construction. Useful when the chat composition root needs to
// inject a closure that captures state created after NewBroker (e.g.
// the chat service's activeSessions map). nil clears the hook.
//
// Safe for concurrent callers: the Broker's mu guards the assignment.
func (b *Broker) SetReplacementSessionHook(hook func(sessionID string, sess *agent.Session)) {
	b.mu.Lock()
	b.replacementHook = hook
	b.mu.Unlock()
}

// activeRetryEntry pairs the bound CancelFunc with the sessionID the
// token was issued for. Stored in Broker.activeRetries; consulted by
// Cancel for session-bound validation.
type activeRetryEntry struct {
	sessionID string
	cancel    context.CancelFunc
}

// CancelRetry aborts an in-flight retry identified by token without
// validating which session the token was issued for. Retained for
// in-process callers that already trust the token (e.g. tests, broker
// internals); production HTTP callers should prefer Cancel which
// enforces session-scoped validation.
//
// Phase 9 wires the FE-facing side via Cancel.
func (b *Broker) CancelRetry(token string) {
	if token == "" {
		return
	}
	b.mu.Lock()
	entry, ok := b.activeRetries[token]
	if ok {
		delete(b.activeRetries, token)
	}
	b.mu.Unlock()
	if ok && entry.cancel != nil {
		entry.cancel()
	}
}

// Cancel aborts the in-flight retry identified by token IFF the token
// was issued for sessionID. Returns true when a cancel actually ran;
// false for unknown tokens, mismatched sessionIDs, empty inputs, or
// already-cancelled tokens. Used by the BE recovery-cancel HTTP
// endpoint to enforce per-session token ownership: a token leaked or
// replayed from another session is silently rejected.
//
// Cancel is the natural completion of Envelope.CancelToken — info-card
// envelopes carry the token as a wrap-level affordance, the FE round-
// trips it back via POST /api/sessions/{id}/recovery/cancel, the
// handler resolves the broker and calls Cancel(sessionID, token).
func (b *Broker) Cancel(sessionID, token string) bool {
	if sessionID == "" || token == "" {
		return false
	}
	b.mu.Lock()
	entry, ok := b.activeRetries[token]
	if !ok {
		b.mu.Unlock()
		return false
	}
	if entry.sessionID != sessionID {
		// Token exists but was issued for a different session — refuse
		// to cancel and leave the entry intact so the legitimate session
		// can still cancel it.
		b.mu.Unlock()
		return false
	}
	delete(b.activeRetries, token)
	b.mu.Unlock()
	if entry.cancel != nil {
		entry.cancel()
		return true
	}
	return false
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

// DispatchRetry dispatches a replacement session for a failed
// FailureEvent. Called from the Phase 6 orchestration once Classify +
// (optional) Remediate decide a retry is appropriate. Exported so
// tests can drive the dispatch path directly without reconstructing
// the full classify→remediate pipeline.
//
// Lineage preservation:
//   - Same SessionID (chat session row, history, slot state, path
//     grants all carry through).
//   - Same AgentProfile, ParentSessionID, Workdir.
//   - Mode derived from ev.Mode (string form persisted on the failed
//     RuntimeRow, mapped back to agent.Mode).
//
// Pre-Boot, the broker transitions the failed RuntimeRow to
// state="launching" via Store.MarkRuntimeRelaunching. NB: this is a
// soft contract — the production agent.Boot path also calls
// Store.CreateRuntimeRow with the same SessionID, which on most
// store backends would conflict with the existing row. The composition
// root in Phase 7 is responsible for either (a) extending agent.Options
// with an IsRelaunch flag that suppresses CreateRuntimeRow on retry,
// or (b) making the Store implementation upsert-aware. Documented as
// a Phase 7 follow-up in the implementer report.
func (b *Broker) DispatchRetry(ctx context.Context, ev *FailureEvent) (*agent.Session, error) {
	if ev == nil {
		return nil, errNilFailureEvent
	}
	if b.deps.AgentBoot == nil {
		return nil, errNoAgentBootWired
	}
	if b.deps.Store != nil {
		if err := b.deps.Store.MarkRuntimeRelaunching(ev.SessionID, "broker retry attempt "+itoa(ev.Attempt)); err != nil {
			return nil, err
		}
	}
	return b.deps.AgentBoot.Boot(ctx, agent.Options{
		Mode:            parseMode(ev.Mode),
		SessionID:       ev.SessionID,
		AgentProfile:    ev.AgentProfile,
		ParentSessionID: ev.LineageOf,
		Workdir:         ev.Workdir,
	})
}

// parseMode maps the string form of agent.Mode (as persisted on the
// RuntimeRow) back to the typed enum. Mirrors agent.Mode.String() and
// defaults to ModeLongLived for unknown inputs — long-lived is the
// most common chat case, and the defensive default makes mid-flight
// schema drift fail soft (replacement session boots in the safest mode).
func parseMode(s string) agent.Mode {
	switch s {
	case "long_lived", "":
		return agent.ModeLongLived
	case "one_shot":
		return agent.ModeOneShot
	case "resume":
		return agent.ModeResume
	case "subagent":
		return agent.ModeSubagent
	case "background":
		return agent.ModeBackground
	default:
		return agent.ModeLongLived
	}
}

// itoa is a tiny zero-alloc-ish int formatter for the relaunch reason
// string. Avoids a strconv import for the single use site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// errNilFailureEvent is returned by DispatchRetry when the caller
// hands it a nil event. Defensive guard against orchestration bugs.
var errNilFailureEvent = brokerErr("recovery.DispatchRetry: nil failure event")

// notifyReplacement invokes the replacementHook (when set) with the
// freshly booted replacement session. nil-safe on every front: a nil
// hook, a nil session, or a panicking hook are all swallowed with a
// log so the orchestration loop's recordOutcome step still runs.
func (b *Broker) notifyReplacement(sessionID string, sess *agent.Session) {
	if sess == nil {
		return
	}
	b.mu.Lock()
	hook := b.replacementHook
	b.mu.Unlock()
	if hook == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			b.logger().Warn("recovery: replacement-session hook panicked",
				"session_id", sessionID, "panic", r)
		}
	}()
	hook(sessionID, sess)
}
