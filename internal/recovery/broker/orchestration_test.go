package broker

import (
	"sync"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

// fakeEnvelope captures emitted envelopes per session so tests can
// assert on Kind/Title/CancelToken.
type fakeEnvelope struct {
	mu        sync.Mutex
	bySession map[string][]Envelope
	emitErr   error
}

func newFakeEnvelope() *fakeEnvelope {
	return &fakeEnvelope{bySession: make(map[string][]Envelope)}
}

func (f *fakeEnvelope) Emit(sessionID string, env Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bySession[sessionID] = append(f.bySession[sessionID], env)
	return f.emitErr
}

func (f *fakeEnvelope) get(sessionID string) []Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Envelope(nil), f.bySession[sessionID]...)
}

// TestOnSessionExitTransientRetrySuccess exercises the happy path:
// classifier returns Transient, broker emits info-card, dispatches
// retry, breadcrumb records OutcomeTransientRetrySucceeded.
func TestOnSessionExitTransientRetrySuccess(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     store,
		Envelope:  envSink,
	})

	// idle_timeout maps to Transient/None.
	exit := &agentsessions.ExitError{Cause: agentsessions.CauseIdleTimeout}
	b.OnSessionExit("sess-1", exit, map[string]any{
		MetaKeyAgentProfile: "claude-sonnet",
		MetaKeyProvider:     "claude",
		MetaKeyMode:         "long_lived",
		MetaKeyWorkdir:      "/proj",
	})

	// Envelope emitted: info-card "Reconnecting".
	envs := envSink.get("sess-1")
	if len(envs) != 1 {
		t.Fatalf("envelopes for sess-1: got %d, want 1", len(envs))
	}
	if envs[0].Kind != "info-card" {
		t.Errorf("envelope Kind = %q, want info-card", envs[0].Kind)
	}

	// Boot dispatched with same SessionID.
	if len(boot.gotOpts) != 1 {
		t.Fatalf("AgentBoot.Boot calls: got %d, want 1", len(boot.gotOpts))
	}
	if boot.gotOpts[0].SessionID != "sess-1" {
		t.Errorf("dispatch SessionID = %q, want sess-1", boot.gotOpts[0].SessionID)
	}

	// Breadcrumb: TransientRetrySucceeded.
	if len(store.breadcrumbs) != 1 {
		t.Fatalf("breadcrumbs: got %d, want 1", len(store.breadcrumbs))
	}
	bc := store.breadcrumbs[0]
	if bc.Class != ClassTransient {
		t.Errorf("breadcrumb Class = %v, want ClassTransient", bc.Class)
	}
	if bc.Outcome != OutcomeTransientRetrySucceeded {
		t.Errorf("breadcrumb Outcome = %v, want OutcomeTransientRetrySucceeded", bc.Outcome)
	}
	if bc.AttemptCount != 1 {
		t.Errorf("breadcrumb AttemptCount = %d, want 1", bc.AttemptCount)
	}
}

// TestOnSessionExitConfigFixedFlow exercises the
// ClassConfigPermissions path: emit info-card → run remediation →
// dispatch retry → record OutcomeRemediated.
func TestOnSessionExitConfigFixedFlow(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	bd := &fakeBootDir{}
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     store,
		Envelope:  envSink,
		BootDir:   bd,
	})

	// watchdog_kill maps to ConfigPermissions/RegenerateCLAUDEMD.
	exit := &agentsessions.ExitError{Cause: agentsessions.CauseWatchdogKill}
	b.OnSessionExit("sess-2", exit, map[string]any{
		MetaKeyAgentProfile: "claude-sonnet",
		MetaKeyProvider:     "claude",
		MetaKeyMode:         "long_lived",
	})

	// Remediator was called.
	if bd.regenCount != 1 {
		t.Errorf("RegenerateCLAUDEMD calls: got %d, want 1", bd.regenCount)
	}

	// Envelope emitted: info-card with "Fixed an agent issue" title.
	envs := envSink.get("sess-2")
	if len(envs) != 1 {
		t.Fatalf("envelopes: got %d, want 1", len(envs))
	}
	if envs[0].Title != "Fixed an agent issue" {
		t.Errorf("Title = %q, want %q", envs[0].Title, "Fixed an agent issue")
	}

	// Breadcrumb: Remediated.
	if len(store.breadcrumbs) != 1 {
		t.Fatalf("breadcrumbs: got %d", len(store.breadcrumbs))
	}
	if got := store.breadcrumbs[0].Outcome; got != OutcomeRemediated {
		t.Errorf("Outcome = %v, want OutcomeRemediated", got)
	}
}

// TestOnSessionExitPermanentSurfacesError exercises the Permanent
// path: emit error-report, no Boot, breadcrumb Outcome=Permanent.
func TestOnSessionExitPermanentSurfacesError(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     store,
		Envelope:  envSink,
	})

	// restart_exhausted -> Permanent (chat-loop-terminated kind).
	exit := &agentsessions.ExitError{Cause: agentsessions.CauseRestartExhausted}
	b.OnSessionExit("sess-3", exit, nil)

	if len(boot.gotOpts) != 0 {
		t.Errorf("Boot should not be called on Permanent, got %d calls", len(boot.gotOpts))
	}

	envs := envSink.get("sess-3")
	if len(envs) != 1 {
		t.Fatalf("envelopes: got %d", len(envs))
	}
	if envs[0].Kind != "chat-loop-terminated" {
		t.Errorf("Kind = %q, want chat-loop-terminated", envs[0].Kind)
	}

	if len(store.breadcrumbs) != 1 {
		t.Fatalf("breadcrumbs: got %d", len(store.breadcrumbs))
	}
	if got := store.breadcrumbs[0].Outcome; got != OutcomePermanent {
		t.Errorf("Outcome = %v, want OutcomePermanent", got)
	}
}

// TestOnSessionExitHardCapEscalates exercises the broker hard cap:
// after maxRetries failures on the same SessionID, the next
// OnSessionExit short-circuits to Permanent without consulting
// the classifier.
func TestOnSessionExitHardCapEscalates(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     store,
		Envelope:  envSink,
	}, WithMaxRetries(2))

	exit := &agentsessions.ExitError{Cause: agentsessions.CauseIdleTimeout}
	cliProviderMeta := map[string]any{MetaKeyProvider: "claude"}

	// 1st failure: Transient retry succeeds (attempt counter -> 1).
	b.OnSessionExit("sess-cap", exit, cliProviderMeta)
	// 2nd failure: Transient retry succeeds (attempt counter -> 2).
	b.OnSessionExit("sess-cap", exit, cliProviderMeta)
	// 3rd failure: hard cap (>2) -> Permanent without dispatch.
	b.OnSessionExit("sess-cap", exit, cliProviderMeta)

	// Two retries should have dispatched (attempts 1 and 2);
	// attempt 3 escalates without dispatch.
	if len(boot.gotOpts) != 2 {
		t.Errorf("Boot calls: got %d, want 2 (attempt 3 should not dispatch)", len(boot.gotOpts))
	}

	// Three breadcrumbs total.
	if len(store.breadcrumbs) != 3 {
		t.Fatalf("breadcrumbs: got %d, want 3", len(store.breadcrumbs))
	}
	last := store.breadcrumbs[2]
	if last.Outcome != OutcomePermanent {
		t.Errorf("3rd breadcrumb Outcome = %v, want OutcomePermanent", last.Outcome)
	}
	if last.AttemptCount != 3 {
		t.Errorf("3rd AttemptCount = %d, want 3", last.AttemptCount)
	}
}

// TestOnSessionExitRemediationFailureEscalates exercises the
// remediation-failed-then-permanent path: classifier returns
// ConfigPermissions, remediator returns an error, broker escalates
// to Permanent and writes a breadcrumb capturing the failed
// remediation in Reason.
func TestOnSessionExitRemediationFailureEscalates(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	bd := &fakeBootDir{regenErr: errBootDirFailed}
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     store,
		Envelope:  envSink,
		BootDir:   bd,
	})

	exit := &agentsessions.ExitError{Cause: agentsessions.CauseWatchdogKill}
	b.OnSessionExit("sess-rem-fail", exit, nil)

	// Boot should NOT have been called — remediation failed before dispatch.
	if len(boot.gotOpts) != 0 {
		t.Errorf("Boot should not be dispatched after remediation failure, got %d calls", len(boot.gotOpts))
	}

	// Two envelopes: the original info-card "Fixed an agent issue", then
	// the escalation error-report.
	envs := envSink.get("sess-rem-fail")
	if len(envs) != 2 {
		t.Fatalf("envelopes: got %d, want 2", len(envs))
	}
	if envs[0].Kind != "info-card" {
		t.Errorf("first env Kind = %q, want info-card", envs[0].Kind)
	}
	if envs[1].Kind != "error-report" {
		t.Errorf("second env Kind = %q, want error-report", envs[1].Kind)
	}

	// Final breadcrumb: Permanent with remediation-failed reason.
	if len(store.breadcrumbs) == 0 {
		t.Fatal("expected at least one breadcrumb")
	}
	last := store.breadcrumbs[len(store.breadcrumbs)-1]
	if last.Outcome != OutcomePermanent {
		t.Errorf("final Outcome = %v, want OutcomePermanent", last.Outcome)
	}
}

var errBootDirFailed = brokerErr("boot dir disk full")

// TestOnSessionExitNilExitNoOp — passing nil exit must not panic and
// must not write a breadcrumb (clean exit, nothing to recover).
func TestOnSessionExitNilExitNoOp_Phase6(t *testing.T) {
	store := &fakeStore{}
	b := NewBroker(Dependencies{Store: store})
	b.OnSessionExit("sess-nil", nil, nil)
	if len(store.breadcrumbs) != 0 {
		t.Errorf("breadcrumbs should be empty for nil exit, got %d", len(store.breadcrumbs))
	}
}

// TestBuildFailureEventReadsMeta verifies the meta-bag projection
// pulls every documented MetaKey* field into the right
// FailureEvent slot. Type mismatches degrade silently (zero value).
func TestBuildFailureEventReadsMeta(t *testing.T) {
	exit := &agentsessions.ExitError{Code: 1}
	meta := map[string]any{
		MetaKeyAgentProfile: "claude-sonnet",
		MetaKeyProvider:     "claude",
		MetaKeyMode:         "subagent",
		MetaKeyLineageOf:    "parent-sess",
		MetaKeyWorkdir:      "/work",
		MetaKeyStderrTail:   "401 unauthorized",
		MetaKeySandboxState: SandboxState{Path: "/sandbox", Missing: true},
		MetaKeyMCPState:     MCPState{Down: true},
		MetaKeyPathGrants:   []string{"/work", "/cache"},
		MetaKeySessionAge:   30 * time.Second,
		MetaKeyLastTurnPos:  42,
	}
	ev := buildFailureEvent("sess-x", exit, meta, 2)

	if ev.SessionID != "sess-x" {
		t.Errorf("SessionID: got %q", ev.SessionID)
	}
	if ev.Attempt != 2 {
		t.Errorf("Attempt: got %d, want 2", ev.Attempt)
	}
	if ev.AgentProfile != "claude-sonnet" {
		t.Errorf("AgentProfile: got %q", ev.AgentProfile)
	}
	if ev.Mode != "subagent" {
		t.Errorf("Mode: got %q", ev.Mode)
	}
	if ev.LineageOf != "parent-sess" {
		t.Errorf("LineageOf: got %q", ev.LineageOf)
	}
	if !ev.SandboxDirState.Missing {
		t.Error("SandboxDirState.Missing should be true")
	}
	if !ev.MCPTransport.Down {
		t.Error("MCPTransport.Down should be true")
	}
	if len(ev.PathGrants) != 2 {
		t.Errorf("PathGrants len: got %d, want 2", len(ev.PathGrants))
	}
	if ev.SessionAge != 30*time.Second {
		t.Errorf("SessionAge: got %v, want 30s", ev.SessionAge)
	}
	if ev.LastTurnPos != 42 {
		t.Errorf("LastTurnPos: got %d, want 42", ev.LastTurnPos)
	}
	if ev.StderrTail != "401 unauthorized" {
		t.Errorf("StderrTail: got %q", ev.StderrTail)
	}
}

// TestBuildFailureEventToleratesBadTypes — meta values with the wrong
// type drop silently (zero value on the FailureEvent), no panic.
func TestBuildFailureEventToleratesBadTypes(t *testing.T) {
	meta := map[string]any{
		MetaKeyAgentProfile: 12345, // wrong type — should drop
		MetaKeySandboxState: "not-a-sandbox-state",
		MetaKeySessionAge:   "not-a-duration",
	}
	ev := buildFailureEvent("s", &agentsessions.ExitError{}, meta, 1)
	if ev.AgentProfile != "" {
		t.Errorf("AgentProfile should drop on type mismatch, got %q", ev.AgentProfile)
	}
	if ev.SandboxDirState.Path != "" {
		t.Error("SandboxDirState should drop on type mismatch")
	}
	if ev.SessionAge != 0 {
		t.Errorf("SessionAge should drop on type mismatch, got %v", ev.SessionAge)
	}
}

// TestClearSessionDropsState — broker.ClearSession is called by the
// chat composition root on chat-session shutdown. Verifies the
// per-session attempt counter resets.
func TestClearSessionDropsState(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     store,
		Envelope:  envSink,
	}, WithMaxRetries(5))

	exit := &agentsessions.ExitError{Cause: agentsessions.CauseIdleTimeout}
	cliProviderMeta := map[string]any{MetaKeyProvider: "claude"}
	b.OnSessionExit("sess-clear", exit, cliProviderMeta) // attempt=1
	b.OnSessionExit("sess-clear", exit, cliProviderMeta) // attempt=2
	b.ClearSession("sess-clear")
	b.OnSessionExit("sess-clear", exit, cliProviderMeta) // should reset to attempt=1

	if got := len(store.breadcrumbs); got != 3 {
		t.Fatalf("breadcrumbs: got %d, want 3", got)
	}
	if a := store.breadcrumbs[2].AttemptCount; a != 1 {
		t.Errorf("post-clear attempt count: got %d, want 1", a)
	}
}
