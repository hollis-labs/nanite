package service

// Tests for CW-20260512-0001: persistPartialAssistantAndNotifyBroker —
// the helper that funnels every chat-HTTP-stream error path through the
// recovery broker (in addition to persisting the [generation interrupted]
// row that the prior persistPartialAssistant helper already wrote).
//
// The acceptance criterion calls for "a unit test covering at least one
// HTTP-stream error class (timeout / transport / http-5xx)" — this file
// covers all four cause families (timeout, transport, rate-budget, generic)
// plus the nil-recovery degraded path.
//
// CW-20260815-0024 added an unconditional skip of the broker call for any
// provider with no implemented bootdir Layout (agent.HasBootdirLayout) —
// every plain HTTP API provider (anthropic, openai, gemini-api,
// openrouter, ...) always failed a subsequent DispatchRetry/agent.Boot
// attempt structurally, producing a guaranteed "permanent failure"
// breadcrumb with zero recovery value.
//
// Phase 0 task 04 (decision log §19) replaced that stopgap with a real
// bootdir-free HTTP retry path: broker.Broker.DispatchRetry now
// branches on HasBootdirLayout itself and never reaches AgentBoot.Boot
// for a no-bootdir-layout provider, so the guard here is gone —
// notifyRecoveryBrokerForHTTPStreamError notifies the broker
// unconditionally again. TestPersistPartialAssistantAndNotifyBroker_NotifiesRegardlessOfBootdirLayout
// covers this directly; the classification-mapping tests below (*Class,
// *MetaBagFields) use a bootable provider name (claude / codex / opencode)
// purely as an arbitrary stand-in — the notify call no longer depends on
// provider shape at all.

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// canonicalHTTPChatMode is the canonical agent.Mode string the
// broker-notify helper writes into broker.MetaKeyMode for HTTP chat
// observations. Surfacing it as a test-side constant pins the contract
// (parseMode must round-trip this value) instead of duplicating the
// magic string across cases.
var canonicalHTTPChatMode = runtimeagent.ModeOneShot.String()

// recordingRecoveryHooks captures OnSessionExit invocations so tests can
// assert the synthesized ExitError + meta bag are correctly shaped.
// Satisfies runtimeagent.RecoveryHooks.
type recordingRecoveryHooks struct {
	mu    sync.Mutex
	wg    sync.WaitGroup
	calls []recoveryHookCall
}

type recoveryHookCall struct {
	sessionID string
	exit      *agentsessions.ExitError
	meta      map[string]any
}

func newRecordingRecoveryHooks() *recordingRecoveryHooks {
	return &recordingRecoveryHooks{}
}

func (r *recordingRecoveryHooks) OnRestart(string, int, *agentsessions.ExitError) {}

func (r *recordingRecoveryHooks) OnSessionExit(sessionID string, exit *agentsessions.ExitError, meta map[string]any) {
	defer r.wg.Done()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recoveryHookCall{sessionID: sessionID, exit: exit, meta: meta})
}

// expect arms the WaitGroup for n upcoming OnSessionExit invocations so
// tests can synchronize with the safego goroutine the helper dispatches.
func (r *recordingRecoveryHooks) expect(n int) {
	r.wg.Add(n)
}

// waitFor blocks until all armed invocations complete (or the test
// times out). Fails the test on timeout so a missing broker notify
// surfaces as a clear failure rather than a flaky test.
func (r *recordingRecoveryHooks) waitFor(t *testing.T, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for OnSessionExit invocation")
	}
}

func (r *recordingRecoveryHooks) snapshot() []recoveryHookCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recoveryHookCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestPersistPartialAssistantAndNotifyBroker_TimeoutClass — ctx-deadline
// errors arriving at the chat-loop deadline check (chat_generate.go:739)
// should synthesize cause=http_stream_timeout, error_class=timeout, and
// thread provider into the broker meta bag.
func TestPersistPartialAssistantAndNotifyBroker_TimeoutClass(t *testing.T) {
	cs := &capturingStore{}
	rec := newRecordingRecoveryHooks()
	svc := &chatServiceImpl{
		store: cs,
		agentDeps: &runtimeagent.Dependencies{
			Recovery: rec,
		},
	}

	rec.expect(1)
	svc.persistPartialAssistantAndNotifyBroker(
		context.Background(),
		"sess-timeout", "msg-timeout", "agent-1",
		"partial content",
		"claude",
		"claude-sonnet",
		context.DeadlineExceeded,
	)
	rec.waitFor(t, 2*time.Second)

	if cs.callCount != 1 {
		t.Fatalf("expected partial-assistant persist; got %d CreateMessage calls", cs.callCount)
	}

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 broker notify; got %d", len(calls))
	}
	c := calls[0]
	if c.sessionID != "sess-timeout" {
		t.Errorf("session_id: got %q, want sess-timeout", c.sessionID)
	}
	if c.exit == nil {
		t.Fatal("exit must not be nil")
	}
	if c.exit.Cause != causeHTTPStreamTimeout {
		t.Errorf("cause: got %q, want %q", c.exit.Cause, causeHTTPStreamTimeout)
	}
	if c.exit.Code != -1 {
		t.Errorf("code: got %d, want -1", c.exit.Code)
	}
	if got := c.meta[httpStreamMetaKeyErrorClass]; got != "timeout" {
		t.Errorf("error_class: got %v, want timeout", got)
	}
	if got := c.meta[httpStreamMetaKeySource]; got != httpStreamMetaSource {
		t.Errorf("source: got %v, want %q", got, httpStreamMetaSource)
	}
	if got := c.meta[broker.MetaKeyProvider]; got != "claude" {
		t.Errorf("provider: got %v, want claude", got)
	}
	if got := c.meta[broker.MetaKeyMode]; got != canonicalHTTPChatMode {
		t.Errorf("mode: got %v, want %q (canonical agent.Mode string consumed by broker.parseMode)", got, canonicalHTTPChatMode)
	}
	if got := c.meta[broker.MetaKeyAgentProfile]; got != "claude-sonnet" {
		t.Errorf("agent_profile: got %v, want claude-sonnet (broker remediation pivots on this — UUID or empty silently retargets the default profile)", got)
	}
}

// TestPersistPartialAssistantAndNotifyBroker_TransportClass — typed
// net.OpError (DNS / TCP / TLS surface) maps to cause=http_stream_transport.
func TestPersistPartialAssistantAndNotifyBroker_TransportClass(t *testing.T) {
	cs := &capturingStore{}
	rec := newRecordingRecoveryHooks()
	svc := &chatServiceImpl{
		store: cs,
		agentDeps: &runtimeagent.Dependencies{
			Recovery: rec,
		},
	}

	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	rec.expect(1)
	svc.persistPartialAssistantAndNotifyBroker(
		context.Background(),
		"sess-transport", "msg-transport", "agent-1",
		"",
		"codex",
		"gpt-4",
		opErr,
	)
	rec.waitFor(t, 2*time.Second)

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 broker notify; got %d", len(calls))
	}
	c := calls[0]
	if c.exit.Cause != causeHTTPStreamTransport {
		t.Errorf("cause: got %q, want %q", c.exit.Cause, causeHTTPStreamTransport)
	}
	if got := c.meta[httpStreamMetaKeyErrorClass]; got != "transport" {
		t.Errorf("error_class: got %v, want transport", got)
	}
	// StderrTail must contain the raw error string so the classifier's
	// auth-failure substring rule can fire when applicable.
	if got, _ := c.meta[broker.MetaKeyStderrTail].(string); got == "" {
		t.Errorf("stderr_tail: expected non-empty error string in meta bag")
	}
}

// TestPersistPartialAssistantAndNotifyBroker_HTTP5xxClass — provider
// 5xx error strings map to cause=http_stream_http_status, error_class=http_5xx.
func TestPersistPartialAssistantAndNotifyBroker_HTTP5xxClass(t *testing.T) {
	cs := &capturingStore{}
	rec := newRecordingRecoveryHooks()
	svc := &chatServiceImpl{
		store: cs,
		agentDeps: &runtimeagent.Dependencies{
			Recovery: rec,
		},
	}

	streamErr := errors.New("anthropic API returned HTTP 503 service unavailable")

	rec.expect(1)
	svc.persistPartialAssistantAndNotifyBroker(
		context.Background(),
		"sess-5xx", "msg-5xx", "agent-1",
		"some prior delta content",
		"claude",
		"claude-sonnet",
		streamErr,
	)
	rec.waitFor(t, 2*time.Second)

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 broker notify; got %d", len(calls))
	}
	c := calls[0]
	if c.exit.Cause != causeHTTPStreamHTTPStatus {
		t.Errorf("cause: got %q, want %q", c.exit.Cause, causeHTTPStreamHTTPStatus)
	}
	if got := c.meta[httpStreamMetaKeyErrorClass]; got != "http_5xx" {
		t.Errorf("error_class: got %v, want http_5xx", got)
	}
}

// TestPersistPartialAssistantAndNotifyBroker_RateBudgetClass — the
// llmcontracts.ErrRequestExceedsRateBudget sentinel maps to
// cause=http_stream_rate_budget, error_class=rate_budget.
func TestPersistPartialAssistantAndNotifyBroker_RateBudgetClass(t *testing.T) {
	cs := &capturingStore{}
	rec := newRecordingRecoveryHooks()
	svc := &chatServiceImpl{
		store: cs,
		agentDeps: &runtimeagent.Dependencies{
			Recovery: rec,
		},
	}

	rec.expect(1)
	svc.persistPartialAssistantAndNotifyBroker(
		context.Background(),
		"sess-rate", "msg-rate", "agent-1",
		"",
		"opencode",
		"opencode-mix",
		llmcontracts.ErrRequestExceedsRateBudget,
	)
	rec.waitFor(t, 2*time.Second)

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 broker notify; got %d", len(calls))
	}
	c := calls[0]
	if c.exit.Cause != causeHTTPStreamRateBudget {
		t.Errorf("cause: got %q, want %q", c.exit.Cause, causeHTTPStreamRateBudget)
	}
	if got := c.meta[httpStreamMetaKeyErrorClass]; got != "rate_budget" {
		t.Errorf("error_class: got %v, want rate_budget", got)
	}
}

// TestPersistPartialAssistantAndNotifyBroker_NoRecoveryDegradesCleanly
// pins the nil-Recovery path: the helper still persists the partial
// assistant row but skips the broker call (no panic, no goroutine leak).
func TestPersistPartialAssistantAndNotifyBroker_NoRecoveryDegradesCleanly(t *testing.T) {
	cs := &capturingStore{}
	// Two sub-cases: agentDeps nil entirely, and agentDeps non-nil with
	// Recovery nil. Both should degrade to a plain persist call.
	cases := []struct {
		name string
		deps *runtimeagent.Dependencies
	}{
		{name: "agentDeps nil", deps: nil},
		{name: "Recovery nil", deps: &runtimeagent.Dependencies{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &chatServiceImpl{store: cs, agentDeps: tc.deps}
			before := cs.callCount
			svc.persistPartialAssistantAndNotifyBroker(
				context.Background(),
				"sess-degraded", "msg-degraded", "agent-1",
				"content",
				"anthropic",
				"claude-sonnet",
				errors.New("boom"),
			)
			if cs.callCount != before+1 {
				t.Errorf("expected partial-assistant persist; got delta %d", cs.callCount-before)
			}
		})
	}
}

// TestPersistPartialAssistantAndNotifyBroker_NotifiesRegardlessOfBootdirLayout
// is the updated regression pin for CW-20260815-0024 / Phase 0 task 04
// (decision log §19). The original guard here unconditionally skipped
// broker notification for any provider with no implemented bootdir
// Layout — DispatchRetry always attempted a doomed agent.Boot for those,
// producing nothing but a guaranteed "permanent failure" breadcrumb.
//
// That guarantee ("never dispatch a doomed CLI-boot retry for a
// no-bootdir-layout provider") now lives INSIDE broker.Broker.DispatchRetry
// itself (it branches on runtimeagent.HasBootdirLayout before ever
// touching AgentBoot.Boot), not at this call site — so this helper no
// longer needs its own skip-guard. Every provider, bootdir-layout or
// not, now reaches the broker: HTTP-provider sessions get classified,
// breadcrumbed, and routed to the new bootdir-free HTTP retry path
// instead of being silently dropped with zero recovery attention.
func TestPersistPartialAssistantAndNotifyBroker_NotifiesRegardlessOfBootdirLayout(t *testing.T) {
	providers := []string{
		"anthropic",
		"openai",
		"gemini-api",
		"openrouter",
		"gemini", // CLI tool name, but no Layout implemented yet
		"claude",
		"pty-claude", // CLI alias — normalizes to "claude"
		"codex",
		"opencode",
	}
	for _, providerName := range providers {
		t.Run(providerName, func(t *testing.T) {
			cs := &capturingStore{}
			rec := newRecordingRecoveryHooks()
			svc := &chatServiceImpl{
				store: cs,
				agentDeps: &runtimeagent.Dependencies{
					Recovery: rec,
				},
			}

			rec.expect(1)
			svc.persistPartialAssistantAndNotifyBroker(
				context.Background(),
				"sess-gate", "msg-gate", "agent-1",
				"content",
				providerName,
				"some-profile",
				errors.New("boom"),
			)
			rec.waitFor(t, 2*time.Second)

			// Partial-assistant persistence must happen either way.
			if cs.callCount != 1 {
				t.Errorf("expected partial-assistant persist; got %d CreateMessage calls", cs.callCount)
			}

			if got := len(rec.snapshot()); got != 1 {
				t.Errorf("provider %q: broker notify calls = %d, want 1", providerName, got)
			}
		})
	}
}

// TestClassifyHTTPStreamError covers the classifier's branches in one
// table-driven sweep. Centralizes the cause / error_class mapping so a
// regression in any branch (timeout, transport, rate-budget, HTTP 5xx,
// auth-substring) surfaces here rather than from the call-site tests.
func TestClassifyHTTPStreamError(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	dnsErr := &net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true}

	cases := []struct {
		name           string
		err            error
		wantCause      string
		wantErrorClass string
	}{
		{"nil error → generic", nil, causeHTTPStream, "unknown"},
		{"context.DeadlineExceeded → timeout", context.DeadlineExceeded, causeHTTPStreamTimeout, "timeout"},
		{"context.Canceled → timeout", context.Canceled, causeHTTPStreamTimeout, "timeout"},
		{"net.OpError → transport", dialErr, causeHTTPStreamTransport, "transport"},
		{"net.DNSError → transport", dnsErr, causeHTTPStreamTransport, "transport"},
		{"rate-budget sentinel → rate_budget", llmcontracts.ErrRequestExceedsRateBudget, causeHTTPStreamRateBudget, "rate_budget"},
		{"429 substring → rate_limit", errors.New("got status 429 from provider"), causeHTTPStreamRateBudget, "rate_limit"},
		{"401 substring → auth", errors.New("401 unauthorized"), causeHTTPStreamHTTPStatus, "auth"},
		{"500 substring → http_5xx", errors.New("provider returned 500 internal server error"), causeHTTPStreamHTTPStatus, "http_5xx"},
		{"timeout substring → timeout", errors.New("read timed out after 30s"), causeHTTPStreamTimeout, "timeout"},
		{"EOF substring → transport", errors.New("unexpected EOF reading stream"), causeHTTPStreamTransport, "transport"},
		{"unrecognized → unknown", errors.New("something strange happened"), causeHTTPStream, "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCause, gotClass := classifyHTTPStreamError(tc.err)
			if gotCause != tc.wantCause {
				t.Errorf("cause: got %q, want %q", gotCause, tc.wantCause)
			}
			if gotClass != tc.wantErrorClass {
				t.Errorf("error_class: got %q, want %q", gotClass, tc.wantErrorClass)
			}
		})
	}
}

// TestPersistPartialAssistantAndNotifyBroker_StandardMetaBagFields pins
// the PR #137 Copilot review-fix contract on the meta bag:
//
//   - MetaKeyAgentProfile MUST carry the agent profile slug (not the
//     agent UUID, not empty). Broker remediations (RefreshCredentials,
//     RepopulateSandbox, etc.) pivot on this slug — an empty value
//     silently falls through to the default profile, and a UUID resolves
//     nothing.
//   - MetaKeyMode MUST be a canonical agent.Mode string (one_shot for
//     HTTP chat). The broker's parseMode helper only understands
//     long_lived / one_shot / resume / subagent / background and
//     silently defaults unknown inputs to long_lived — that would
//     mis-shape any DispatchRetry against an HTTP failure (no
//     long-lived subprocess exists to relaunch).
//   - The HTTP-chat discriminator lives on httpStreamMetaKeySource
//     (source="http_chat_stream"), not on MetaKeyMode.
func TestPersistPartialAssistantAndNotifyBroker_StandardMetaBagFields(t *testing.T) {
	cs := &capturingStore{}
	rec := newRecordingRecoveryHooks()
	svc := &chatServiceImpl{
		store: cs,
		agentDeps: &runtimeagent.Dependencies{
			Recovery: rec,
		},
	}

	rec.expect(1)
	svc.persistPartialAssistantAndNotifyBroker(
		context.Background(),
		"sess-meta", "msg-meta", "agent-uuid-abcdef",
		"partial content",
		"claude",
		"claude-sonnet",
		errors.New("read tcp: connection reset by peer"),
	)
	rec.waitFor(t, 2*time.Second)

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 broker notify; got %d", len(calls))
	}
	c := calls[0]

	// Profile slug, NOT UUID. Compare against the input slug so refactors
	// to the resolver can't silently regress this.
	if got := c.meta[broker.MetaKeyAgentProfile]; got != "claude-sonnet" {
		t.Errorf("MetaKeyAgentProfile: got %v, want %q (profile slug, NOT agent UUID)", got, "claude-sonnet")
	}
	if got := c.meta[broker.MetaKeyAgentProfile]; got == "agent-uuid-abcdef" {
		t.Errorf("MetaKeyAgentProfile must not carry the agent UUID (got %v) — broker remediations resolve by profile slug", got)
	}

	// Mode must round-trip through agent.Mode.String() so the broker's
	// parseMode accepts it without falling back to ModeLongLived. We
	// pin the value AND verify it's one of the canonical strings
	// parseMode understands (defense in depth — if ModeOneShot.String()
	// ever changes, the canonical-set check still flags this).
	gotMode, _ := c.meta[broker.MetaKeyMode].(string)
	if gotMode != canonicalHTTPChatMode {
		t.Errorf("MetaKeyMode: got %q, want %q", gotMode, canonicalHTTPChatMode)
	}
	canonicalModes := map[string]bool{
		"long_lived": true,
		"one_shot":   true,
		"resume":     true,
		"subagent":   true,
		"background": true,
	}
	if !canonicalModes[gotMode] {
		t.Errorf("MetaKeyMode %q is not a canonical agent.Mode string — broker.parseMode will silently default it to long_lived", gotMode)
	}
	if gotMode == "http_chat" {
		t.Errorf("MetaKeyMode must NOT be the legacy 'http_chat' string — broker.parseMode does not recognize it")
	}

	// The http_chat_stream discriminator lives on the source extra key,
	// not on MetaKeyMode.
	if got := c.meta[httpStreamMetaKeySource]; got != httpStreamMetaSource {
		t.Errorf("httpStreamMetaKeySource: got %v, want %q", got, httpStreamMetaSource)
	}
}

// Compile-time assertion that the test fixture satisfies the production
// interface. Keeps the test wiring honest against future Dependencies
// changes (new hooks → recordingRecoveryHooks compiles → test still runs).
var _ runtimeagent.RecoveryHooks = (*recordingRecoveryHooks)(nil)
