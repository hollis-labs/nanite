package broker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/nanite/internal/runtime/agent"
)

// fakeHTTPRetry captures Retry calls and lets tests script the response.
// Mirrors fakeAgentBoot's shape for the HTTP-provider retry contract.
type fakeHTTPRetry struct {
	mu      sync.Mutex
	gotEvs  []*FailureEvent
	gotCtxs []context.Context
	retErr  error
}

func (f *fakeHTTPRetry) Retry(ctx context.Context, ev *FailureEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotEvs = append(f.gotEvs, ev)
	f.gotCtxs = append(f.gotCtxs, ctx)
	return f.retErr
}

func (f *fakeHTTPRetry) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.gotEvs)
}

// TestHTTPRetryBackoff pins the bounded-exponential shape: 1s, 2s, 4s,
// ... capped at the supplied ceiling. Decision log §19 requires real
// backoff (not naive immediate retry) for the HTTP-provider retry path.
func TestHTTPRetryBackoff(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		ceiling time.Duration
		want    time.Duration
	}{
		{"attempt 1, generous ceiling", 1, 10 * time.Second, 1 * time.Second},
		{"attempt 2, generous ceiling", 2, 10 * time.Second, 2 * time.Second},
		{"attempt 3, generous ceiling", 3, 10 * time.Second, 4 * time.Second},
		{"attempt 4 capped at ceiling", 4, 10 * time.Second, 8 * time.Second},
		{"attempt 5 capped at ceiling", 5, 10 * time.Second, 10 * time.Second},
		{"attempt 0 treated as attempt 1", 0, 10 * time.Second, 1 * time.Second},
		{"negative attempt treated as attempt 1", -3, 10 * time.Second, 1 * time.Second},
		{"tight ceiling caps attempt 1 too", 1, 50 * time.Millisecond, 50 * time.Millisecond},
		{"zero ceiling disables cap", 2, 0, 2 * time.Second},
		{"large attempt doesn't overflow", 1000, 10 * time.Second, 10 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := httpRetryBackoff(tc.attempt, tc.ceiling)
			if got != tc.want {
				t.Errorf("httpRetryBackoff(%d, %v) = %v, want %v", tc.attempt, tc.ceiling, got, tc.want)
			}
		})
	}
}

// TestDispatchRetryRoutesHTTPProviderToHTTPRetry pins the core routing
// decision: a no-bootdir-layout provider (anthropic here) never touches
// AgentBoot.Boot and instead calls the wired HTTPRetry hook with the
// same FailureEvent.
func TestDispatchRetryRoutesHTTPProviderToHTTPRetry(t *testing.T) {
	boot := &fakeAgentBoot{}
	httpRetry := &fakeHTTPRetry{}
	b := NewBroker(Dependencies{AgentBoot: boot, HTTPRetry: httpRetry},
		WithRemediationTimeout(20*time.Millisecond)) // keep backoff fast

	ev := &FailureEvent{SessionID: "sess-http", Provider: "anthropic", Attempt: 1}
	if _, err := b.DispatchRetry(context.Background(), ev); err != nil {
		t.Fatalf("DispatchRetry: unexpected err = %v", err)
	}

	if len(boot.gotOpts) != 0 {
		t.Errorf("AgentBoot.Boot must not be called for a no-bootdir-layout provider, got %d calls", len(boot.gotOpts))
	}
	if httpRetry.calls() != 1 {
		t.Fatalf("HTTPRetry.Retry calls: got %d, want 1", httpRetry.calls())
	}
	if httpRetry.gotEvs[0] != ev {
		t.Errorf("HTTPRetry.Retry got a different *FailureEvent than DispatchRetry received")
	}
}

// TestDispatchRetryHTTPReturnsNilSession — the HTTP-provider retry path
// has no *agent.Session (bootdir-free, no subprocess launched), so
// DispatchRetry must return a nil session even on success. notifyReplacement
// is nil-safe on this (asserted separately via the orchestration test).
func TestDispatchRetryHTTPReturnsNilSession(t *testing.T) {
	httpRetry := &fakeHTTPRetry{}
	b := NewBroker(Dependencies{HTTPRetry: httpRetry}, WithRemediationTimeout(20*time.Millisecond))

	sess, err := b.DispatchRetry(context.Background(), &FailureEvent{SessionID: "s", Provider: "openai", Attempt: 1})
	if err != nil {
		t.Fatalf("DispatchRetry: unexpected err = %v", err)
	}
	if sess != nil {
		t.Errorf("DispatchRetry session = %v, want nil for HTTP-provider retry", sess)
	}
}

// TestDispatchRetryHTTPPropagatesRetryErr — when HTTPRetry.Retry fails,
// the broker surfaces the error so orchestration can escalate, same
// contract as TestDispatchRetryPropagatesBootErr for the CLI path.
func TestDispatchRetryHTTPPropagatesRetryErr(t *testing.T) {
	wantErr := errors.New("no user message found in session")
	httpRetry := &fakeHTTPRetry{retErr: wantErr}
	b := NewBroker(Dependencies{HTTPRetry: httpRetry}, WithRemediationTimeout(20*time.Millisecond))

	_, err := b.DispatchRetry(context.Background(), &FailureEvent{SessionID: "s", Provider: "anthropic", Attempt: 1})
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want wrapping %v", err, wantErr)
	}
}

// TestDispatchRetryHTTPBackoffRespectsCancel verifies a canceled ctx
// aborts the backoff wait promptly (bounded time) instead of blocking
// for the full computed backoff duration, and never calls Retry once
// canceled. Mirrors the FE [Cancel retry] flow, which cancels the ctx
// registered via registerActiveRetry before DispatchRetry runs.
func TestDispatchRetryHTTPBackoffRespectsCancel(t *testing.T) {
	httpRetry := &fakeHTTPRetry{}
	// Generous ceiling so the uncanceled wait would be seconds long —
	// proves the early return is due to ctx cancellation, not the
	// backoff naturally elapsing.
	b := NewBroker(Dependencies{HTTPRetry: httpRetry}, WithRemediationTimeout(10*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := b.DispatchRetry(ctx, &FailureEvent{SessionID: "s", Provider: "anthropic", Attempt: 3})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("DispatchRetry: expected ctx-cancellation error, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("DispatchRetry blocked %v past cancellation (want well under the 4s backoff step)", elapsed)
	}
	if httpRetry.calls() != 0 {
		t.Errorf("HTTPRetry.Retry should not be called once ctx is canceled during backoff, got %d calls", httpRetry.calls())
	}
}

// TestOnSessionExitHTTPProviderTransientRetry is the end-to-end
// functional check for decision log §19: a synthesized HTTP-stream
// failure (mirroring what chat_http_broker_notify.go builds) on a
// no-bootdir-layout provider gets classified, dispatches through the
// new HTTPRetry hook (never AgentBoot), and a breadcrumb records
// OutcomeTransientRetrySucceeded — exactly like the CLI path, just
// through the bootdir-free branch.
func TestOnSessionExitHTTPProviderTransientRetry(t *testing.T) {
	boot := &fakeAgentBoot{}
	httpRetry := &fakeHTTPRetry{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		HTTPRetry: httpRetry,
		Store:     store,
		Envelope:  envSink,
	}, WithRemediationTimeout(20*time.Millisecond))

	// Mirrors notifyRecoveryBrokerForHTTPStreamError's synthesized exit:
	// Code -1, no lib-level Cause, so Classify falls through to the
	// default "Code != 0, first occurrence -> Transient" branch.
	exit := &agentsessions.ExitError{Code: -1, Cause: "http_stream_timeout"}
	b.OnSessionExit("sess-http-1", exit, map[string]any{
		MetaKeyProvider:     "anthropic",
		MetaKeyAgentProfile: "claude-sonnet",
		MetaKeyMode:         "one_shot",
	})

	if len(boot.gotOpts) != 0 {
		t.Errorf("AgentBoot.Boot must never be called for an HTTP-provider session, got %d calls", len(boot.gotOpts))
	}
	if httpRetry.calls() != 1 {
		t.Fatalf("HTTPRetry.Retry calls: got %d, want 1", httpRetry.calls())
	}

	envs := envSink.get("sess-http-1")
	if len(envs) != 1 {
		t.Fatalf("envelopes: got %d, want 1", len(envs))
	}
	if envs[0].Kind != "info-card" {
		t.Errorf("envelope Kind = %q, want info-card", envs[0].Kind)
	}

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
	if bc.Cause != "http_stream_timeout" {
		t.Errorf("breadcrumb Cause = %q, want http_stream_timeout", bc.Cause)
	}
}

func TestRejectedHTTPRequestDoesNotRetry(t *testing.T) {
	retry := &fakeHTTPRetry{}
	store := &fakeStore{}
	envelopes := newFakeEnvelope()
	b := NewBroker(Dependencies{HTTPRetry: retry, Store: store, Envelope: envelopes})
	b.OnSessionExit("rejected", &agentsessions.ExitError{Code: -1, Cause: CauseHTTPRequestRejected}, map[string]any{MetaKeyProvider: "openai"})
	if retry.calls() != 0 {
		t.Fatal("retried an unchanged invalid request")
	}
	if len(store.breadcrumbs) != 1 || store.breadcrumbs[0].Outcome != OutcomePermanent {
		t.Fatalf("missing terminal recovery record: %+v", store.breadcrumbs)
	}
	if len(envelopes.get("rejected")) != 0 {
		t.Fatal("duplicated the existing chat recovery explanation")
	}
}

// TestOnSessionExitHTTPProviderExhaustsToPermanent drives repeated
// HTTP-provider failures past the broker hard cap and confirms the same
// escalation discipline the CLI path already has: bounded attempts, then
// Permanent — an HTTP session does not get unlimited retries.
func TestOnSessionExitHTTPProviderExhaustsToPermanent(t *testing.T) {
	httpRetry := &fakeHTTPRetry{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{
		HTTPRetry: httpRetry,
		Store:     store,
		Envelope:  envSink,
	}, WithMaxRetries(2), WithRemediationTimeout(20*time.Millisecond))

	// Code != 0, no Cause: Classify's default branch is Transient on
	// attempt 1 and Permanent on attempt >= 2 (unclassified-failure
	// escalation) — but the broker's own hard cap (maxRetries=2) is what
	// this test pins, so use a Cause the classifier treats as Transient
	// every time (idle_timeout) to isolate the hard-cap behavior from
	// the classifier's own second-occurrence rule.
	exit := &agentsessions.ExitError{Code: -1, Cause: agentsessions.CauseIdleTimeout}

	b.OnSessionExit("sess-http-cap", exit, map[string]any{MetaKeyProvider: "anthropic"}) // attempt=1: retry
	b.OnSessionExit("sess-http-cap", exit, map[string]any{MetaKeyProvider: "anthropic"}) // attempt=2: retry
	b.OnSessionExit("sess-http-cap", exit, map[string]any{MetaKeyProvider: "anthropic"}) // attempt=3: hard cap -> Permanent

	if httpRetry.calls() != 2 {
		t.Errorf("HTTPRetry.Retry calls: got %d, want 2 (attempt 3 should not dispatch)", httpRetry.calls())
	}
	if len(store.breadcrumbs) != 3 {
		t.Fatalf("breadcrumbs: got %d, want 3", len(store.breadcrumbs))
	}
	last := store.breadcrumbs[2]
	if last.Outcome != OutcomePermanent {
		t.Errorf("3rd breadcrumb Outcome = %v, want OutcomePermanent", last.Outcome)
	}
}

// TestOnSessionExitHTTPProviderNoRetryWiredEscalates — when HTTPRetry
// isn't wired (degraded boot, or a test Dependencies literal that only
// cares about the CLI path), an HTTP-provider failure still gets a
// breadcrumb (unlike the pre-fix behavior, which skipped the broker
// entirely and produced zero breadcrumbs for these sessions) — it just
// escalates straight to Permanent instead of retrying.
func TestOnSessionExitHTTPProviderNoRetryWiredEscalates(t *testing.T) {
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{Store: store, Envelope: envSink}) // no HTTPRetry

	exit := &agentsessions.ExitError{Code: -1, Cause: agentsessions.CauseIdleTimeout}
	b.OnSessionExit("sess-http-unwired", exit, map[string]any{MetaKeyProvider: "anthropic"})

	if len(store.breadcrumbs) != 1 {
		t.Fatalf("breadcrumbs: got %d, want 1", len(store.breadcrumbs))
	}
	if got := store.breadcrumbs[0].Outcome; got != OutcomePermanent {
		t.Errorf("Outcome = %v, want OutcomePermanent", got)
	}

	// Two envelopes, same shape as the CLI-side remediation-failure case
	// (TestOnSessionExitRemediationFailureEscalates): the original
	// info-card "Reconnecting agent" (idle_timeout classifies Transient,
	// which always announces the retry attempt first), then the
	// escalation error-report once DispatchRetry itself fails.
	envs := envSink.get("sess-http-unwired")
	if len(envs) != 2 {
		t.Fatalf("envelopes: got %d, want 2: %+v", len(envs), envs)
	}
	if envs[0].Kind != "info-card" {
		t.Errorf("first env Kind = %q, want info-card", envs[0].Kind)
	}
	if envs[1].Kind != "error-report" {
		t.Errorf("second env Kind = %q, want error-report", envs[1].Kind)
	}
}

// TestReplacementSessionHook_NotFiredForHTTPRetry — the replacement
// hook exists to adopt a booted *agent.Session into chatServiceImpl's
// activeSessions map. HTTP-provider retries never produce a session (no
// subprocess launched), so the hook must not fire for them even on a
// successful dispatch.
func TestReplacementSessionHook_NotFiredForHTTPRetry(t *testing.T) {
	httpRetry := &fakeHTTPRetry{}
	called := 0
	b := NewBroker(Dependencies{
		HTTPRetry: httpRetry,
		Store:     &fakeStore{},
		Envelope:  &nopEnvelope{},
	},
		WithRemediationTimeout(20*time.Millisecond),
		WithReplacementSessionHook(func(string, *agent.Session) { called++ }),
	)

	exit := &agentsessions.ExitError{Cause: agentsessions.CauseIdleTimeout}
	b.OnSessionExit("sess-http-hook", exit, map[string]any{MetaKeyProvider: "anthropic"})

	if called != 0 {
		t.Errorf("replacement hook should not fire for an HTTP-provider retry (no session to adopt); got %d calls", called)
	}
}
