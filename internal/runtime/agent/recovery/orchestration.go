package recovery

import (
	"context"
	"fmt"
	"time"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
)

// Standard meta keys the chat composition root populates on the
// OnSessionExit meta bag. The broker reads what's available and
// degrades gracefully when keys are absent (zero-valued FailureEvent
// fields).
const (
	MetaKeyAgentProfile    = "agent_profile"
	MetaKeyProvider        = "provider"
	MetaKeyMode            = "mode"
	MetaKeyLineageOf       = "lineage_of"
	MetaKeyWorkdir         = "workdir"
	MetaKeyStderrTail      = "stderr_tail"
	MetaKeySandboxState    = "sandbox_state"     // SandboxState value
	MetaKeyMCPState        = "mcp_state"         // MCPState value
	MetaKeyPathGrants      = "path_grants"       // []string
	MetaKeySessionAge      = "session_age"       // time.Duration
	MetaKeyLastTurnPos     = "last_turn_pos"     // int
)

// OnSessionExit is the broker's main orchestration entry point.
// Invoked from the chat composition root's Wait-observer goroutine
// when a session's terminal *agentsessions.ExitError lands.
//
// Phase 6 fully wires this: classify → (remediate, if applicable) →
// dispatch retry → render+emit user-facing envelope → write breadcrumb.
// Hard cap (broker-level Attempt > maxRetries) short-circuits to
// Permanent. Cancel-retry tokens registered on info-card envelopes
// route the FE's cancel_retry event back to broker.CancelRetry, which
// aborts the in-flight retry context.
//
// The orchestration is synchronous on the calling goroutine —
// remediation + dispatch run in line. The chat composition root's
// Wait-observer is single-purpose, so blocking is fine.
func (b *Broker) OnSessionExit(sessionID string, exit *agentsessions.ExitError, meta map[string]any) {
	logger := b.logger()
	if exit == nil {
		logger.Info("recovery: nil exit skipped", "session_id", sessionID)
		return
	}

	// Increment the per-session attempt counter under the lock and
	// snapshot. attemptCount is 1-indexed: first failure observation
	// increments to 1.
	b.mu.Lock()
	st := b.stateForLocked(sessionID)
	st.attemptCount++
	attempt := st.attemptCount
	b.mu.Unlock()

	ev := buildFailureEvent(sessionID, exit, meta, attempt)
	started := time.Now()

	logger.Info("recovery: terminal exit observed",
		"session_id", sessionID,
		"attempt", attempt,
		"cause", exit.Cause,
		"code", exit.Code,
		"signal", exit.Signal)

	// Hard cap: broker has already retried maxRetries times for this
	// session. Escalate to Permanent without re-classifying.
	if attempt > b.maxRetries {
		b.escalatePermanent(ev, Classification{
			Class:       ClassPermanent,
			Reason:      fmt.Sprintf("broker hard cap (%d attempts) exhausted", b.maxRetries),
			Remediation: RemediationNone,
		}, started)
		return
	}

	classification := Classify(ev)

	switch classification.Class {
	case ClassPermanent:
		b.escalatePermanent(ev, classification, started)
		return

	case ClassConfigPermissions:
		b.runConfigFixedRetry(ev, classification, started)
		return

	case ClassTransient:
		b.runTransientRetry(ev, classification, started)
		return

	default:
		// Unknown class — defensive. Treat as permanent.
		b.escalatePermanent(ev, Classification{
			Class:       ClassPermanent,
			Reason:      "classifier returned unknown class",
			Remediation: RemediationNone,
		}, started)
	}
}

// runConfigFixedRetry executes the ClassConfigPermissions flow:
// emit info-card → remediate → check cancel → dispatch retry → write
// breadcrumb. Failures at any step escalate to Permanent.
func (b *Broker) runConfigFixedRetry(ev *FailureEvent, c Classification, started time.Time) {
	env := b.RenderUserMessage(ev, c, ActionRetryConfigFixed)
	ctx, cancel := context.WithCancel(context.Background())
	b.registerActiveRetry(env.CancelToken, cancel)
	defer b.unregisterActiveRetry(env.CancelToken)
	defer cancel()

	if err := b.emitEnvelope(ev.SessionID, env); err != nil {
		b.logger().Warn("recovery: envelope emit failed", "session_id", ev.SessionID, "err", err)
		// Continue anyway — the user not seeing the info-card is bad
		// UX but the recovery flow can still succeed.
	}

	if err := b.Remediate(ctx, ev, c); err != nil {
		b.logger().Warn("recovery: remediation failed",
			"session_id", ev.SessionID,
			"remediation", c.Remediation,
			"err", err)
		b.escalatePermanent(ev, Classification{
			Class:       ClassPermanent,
			Reason:      fmt.Sprintf("remediation %s failed: %v", c.Remediation, err),
			Remediation: c.Remediation,
		}, started)
		return
	}

	if ctx.Err() != nil {
		// User clicked [Cancel retry] during remediation.
		b.recordOutcome(ev, c, ActionRetryConfigFixed, OutcomeCancelled, started)
		return
	}

	if _, err := b.DispatchRetry(ctx, ev); err != nil {
		b.logger().Warn("recovery: retry dispatch failed",
			"session_id", ev.SessionID,
			"err", err)
		b.escalatePermanent(ev, Classification{
			Class:       ClassPermanent,
			Reason:      fmt.Sprintf("retry dispatch failed after remediation: %v", err),
			Remediation: c.Remediation,
		}, started)
		return
	}

	b.recordOutcome(ev, c, ActionRetryConfigFixed, OutcomeRemediated, started)
}

// runTransientRetry executes the ClassTransient flow: emit info-card
// → check cancel → dispatch retry → write breadcrumb. No remediation.
func (b *Broker) runTransientRetry(ev *FailureEvent, c Classification, started time.Time) {
	env := b.RenderUserMessage(ev, c, ActionRetryTransient)
	ctx, cancel := context.WithCancel(context.Background())
	b.registerActiveRetry(env.CancelToken, cancel)
	defer b.unregisterActiveRetry(env.CancelToken)
	defer cancel()

	if err := b.emitEnvelope(ev.SessionID, env); err != nil {
		b.logger().Warn("recovery: envelope emit failed", "session_id", ev.SessionID, "err", err)
	}

	if ctx.Err() != nil {
		b.recordOutcome(ev, c, ActionRetryTransient, OutcomeCancelled, started)
		return
	}

	if _, err := b.DispatchRetry(ctx, ev); err != nil {
		b.logger().Warn("recovery: retry dispatch failed",
			"session_id", ev.SessionID,
			"err", err)
		b.escalatePermanent(ev, Classification{
			Class:       ClassPermanent,
			Reason:      fmt.Sprintf("retry dispatch failed: %v", err),
			Remediation: RemediationNone,
		}, started)
		return
	}

	b.recordOutcome(ev, c, ActionRetryTransient, OutcomeTransientRetrySucceeded, started)
}

// escalatePermanent emits the permanent-failure envelope and writes
// the breadcrumb. Called from the orchestration whenever a path
// terminates without a successful retry — initial Permanent
// classification, hard cap exhaustion, remediation failure, dispatch
// failure.
func (b *Broker) escalatePermanent(ev *FailureEvent, c Classification, started time.Time) {
	env := b.RenderUserMessage(ev, c, ActionPermanentFailure)
	if err := b.emitEnvelope(ev.SessionID, env); err != nil {
		b.logger().Warn("recovery: permanent envelope emit failed", "session_id", ev.SessionID, "err", err)
	}
	b.recordOutcome(ev, c, ActionPermanentFailure, OutcomePermanent, started)
	// Permanent escalation marks the end of recovery for this session;
	// drop the per-session classifier state.
	b.clearState(ev.SessionID)
}

// recordOutcome writes a breadcrumb capturing the resolved
// Class/Remediation/Action/Outcome triple. Postmortem queries pivot
// off this.
func (b *Broker) recordOutcome(ev *FailureEvent, c Classification, action Action, outcome Outcome, started time.Time) {
	cause := ""
	if ev != nil && ev.Exit != nil {
		cause = ev.Exit.Cause
	}
	b.writeBreadcrumb(Breadcrumb{
		Timestamp:           time.Now(),
		SessionID:           ev.SessionID,
		Class:               c.Class,
		Cause:               cause,
		Remediation:         c.Remediation,
		Action:              action,
		Outcome:             outcome,
		AttemptCount:        ev.Attempt,
		DurationFromFailure: time.Since(started),
		Reason:              c.Reason,
	})
}

// emitEnvelope sends a recovery envelope through the configured
// EnvelopeSink. Nil-Dependencies.Envelope is allowed (degraded mode);
// the broker logs and continues.
func (b *Broker) emitEnvelope(sessionID string, env Envelope) error {
	if b.deps.Envelope == nil {
		b.logger().Warn("recovery: no Envelope sink wired — message dropped",
			"session_id", sessionID,
			"kind", env.Kind,
			"title", env.Title)
		return nil
	}
	return b.deps.Envelope.Emit(sessionID, env)
}

// registerActiveRetry stores the cancel func against an active retry
// token so a FE-driven CancelRetry can find and abort it.
func (b *Broker) registerActiveRetry(token string, cancel context.CancelFunc) {
	if token == "" {
		return
	}
	b.mu.Lock()
	b.activeRetries[token] = cancel
	b.mu.Unlock()
}

// unregisterActiveRetry removes the cancel func once the retry
// completes (success or failure). Safe to call with an unknown token.
func (b *Broker) unregisterActiveRetry(token string) {
	if token == "" {
		return
	}
	b.mu.Lock()
	delete(b.activeRetries, token)
	b.mu.Unlock()
}

// ClearSession removes the per-session classifier state for the named
// session. Called by the chat composition root when a chat session
// ends, so the broker doesn't leak memory for sessions that are no
// longer active.
//
// Safe to call for unknown sessionIDs (no-op).
func (b *Broker) ClearSession(sessionID string) {
	b.clearState(sessionID)
}

// buildFailureEvent constructs a FailureEvent from the OnSessionExit
// inputs + the broker's per-session attempt counter. Pulls structured
// context out of the meta bag using the standard MetaKey* constants;
// missing keys produce zero-valued fields, which the classifier
// handles defensively.
func buildFailureEvent(sessionID string, exit *agentsessions.ExitError, meta map[string]any, attempt int) *FailureEvent {
	ev := &FailureEvent{
		SessionID:  sessionID,
		Exit:       exit,
		Attempt:    attempt,
		OccurredAt: time.Now(),
	}
	if meta == nil {
		return ev
	}

	if v, ok := metaString(meta, MetaKeyAgentProfile); ok {
		ev.AgentProfile = v
	}
	if v, ok := metaString(meta, MetaKeyProvider); ok {
		ev.Provider = v
	}
	if v, ok := metaString(meta, MetaKeyMode); ok {
		ev.Mode = v
	}
	if v, ok := metaString(meta, MetaKeyLineageOf); ok {
		ev.LineageOf = v
	}
	if v, ok := metaString(meta, MetaKeyWorkdir); ok {
		ev.Workdir = v
	}
	if v, ok := metaString(meta, MetaKeyStderrTail); ok {
		ev.StderrTail = v
	}
	if v, ok := meta[MetaKeySandboxState].(SandboxState); ok {
		ev.SandboxDirState = v
	}
	if v, ok := meta[MetaKeyMCPState].(MCPState); ok {
		ev.MCPTransport = v
	}
	if v, ok := meta[MetaKeyPathGrants].([]string); ok {
		ev.PathGrants = v
	}
	if v, ok := meta[MetaKeySessionAge].(time.Duration); ok {
		ev.SessionAge = v
	}
	if v, ok := meta[MetaKeyLastTurnPos].(int); ok {
		ev.LastTurnPos = v
	}
	return ev
}

// metaString pulls a string-typed value from the meta bag. Tolerates
// the value being absent or a non-string type (returns ok=false rather
// than panicking on a bad type assertion).
func metaString(meta map[string]any, key string) (string, bool) {
	v, ok := meta[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}
