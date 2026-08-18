package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/nanite/internal/runtime/agent"
)

// fakeAgentBoot captures Boot calls, lets tests script the response,
// and asserts on the agent.Options the broker passed in.
type fakeAgentBoot struct {
	gotOpts []agent.Options
	retSess *agent.Session
	retErr  error
}

func (f *fakeAgentBoot) Boot(_ context.Context, opts agent.Options) (*agent.Session, error) {
	f.gotOpts = append(f.gotOpts, opts)
	return f.retSess, f.retErr
}

// fakeStore captures MarkRuntimeRelaunching calls so the test can
// assert on the SessionID + reason. WriteBreadcrumb is exercised
// elsewhere; here we just need the no-op to satisfy the interface.
type fakeStore struct {
	relaunchCalls []relaunchCall
	relaunchErr   error

	breadcrumbs []Breadcrumb
}

type relaunchCall struct {
	sessionID string
	reason    string
}

func (f *fakeStore) MarkRuntimeRelaunching(sessionID, reason string) error {
	f.relaunchCalls = append(f.relaunchCalls, relaunchCall{sessionID, reason})
	return f.relaunchErr
}

func (f *fakeStore) WriteBreadcrumb(b Breadcrumb) error {
	f.breadcrumbs = append(f.breadcrumbs, b)
	return nil
}

// TestDispatchRetryPreservesLineage pins the broker's lineage
// preservation contract: the replacement session inherits SessionID,
// AgentProfile, ParentSessionID (from LineageOf), and Workdir from the
// failed FailureEvent. Mode is derived from the persisted string form.
func TestDispatchRetryPreservesLineage(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	b := NewBroker(Dependencies{AgentBoot: boot, Store: store})

	ev := &FailureEvent{
		SessionID:    "sess-abc",
		AgentProfile: "claude-sonnet",
		Provider:     "claude",
		Mode:         "long_lived",
		Attempt:      2,
		LineageOf:    "parent-sess-001",
		Workdir:      "/some/project",
	}

	if _, err := b.DispatchRetry(context.Background(), ev); err != nil {
		t.Fatalf("DispatchRetry: %v", err)
	}

	if len(boot.gotOpts) != 1 {
		t.Fatalf("AgentBoot.Boot calls: got %d, want 1", len(boot.gotOpts))
	}
	got := boot.gotOpts[0]
	if got.SessionID != "sess-abc" {
		t.Errorf("SessionID: got %q, want %q", got.SessionID, "sess-abc")
	}
	if got.AgentProfile != "claude-sonnet" {
		t.Errorf("AgentProfile: got %q", got.AgentProfile)
	}
	if got.ParentSessionID != "parent-sess-001" {
		t.Errorf("ParentSessionID: got %q, want %q", got.ParentSessionID, "parent-sess-001")
	}
	if got.Workdir != "/some/project" {
		t.Errorf("Workdir: got %q", got.Workdir)
	}
	if got.Mode != agent.ModeLongLived {
		t.Errorf("Mode: got %v, want ModeLongLived", got.Mode)
	}
	// CW-20260526-0002: provider is load-bearing on the replacement Boot
	// so a CLI exit on `claude` retries on `claude`, not the default
	// provider implied by the agent profile.
	if got.Provider != "claude" {
		t.Errorf("Provider: got %q, want claude", got.Provider)
	}

	// Store transition must precede the Boot call. With our fake the
	// only signal we have is that both happened; ordering is implicit
	// in the broker code (Mark before Boot).
	if len(store.relaunchCalls) != 1 {
		t.Fatalf("MarkRuntimeRelaunching: got %d calls, want 1", len(store.relaunchCalls))
	}
	if store.relaunchCalls[0].sessionID != "sess-abc" {
		t.Errorf("relaunch sessionID: got %q", store.relaunchCalls[0].sessionID)
	}
	if !strings.Contains(store.relaunchCalls[0].reason, "broker retry") {
		t.Errorf("relaunch reason should mention broker retry, got %q", store.relaunchCalls[0].reason)
	}
	if !strings.Contains(store.relaunchCalls[0].reason, "2") {
		t.Errorf("relaunch reason should include attempt number 2, got %q", store.relaunchCalls[0].reason)
	}
}

// TestDispatchRetryDerivesMode pins the Mode parsing — every value
// that agent.Mode.String() emits must round-trip through parseMode +
// be preserved on the dispatched Boot options.
func TestDispatchRetryDerivesMode(t *testing.T) {
	cases := []struct {
		name string
		mode string
		want agent.Mode
	}{
		{"long_lived", "long_lived", agent.ModeLongLived},
		{"empty defaults to long_lived", "", agent.ModeLongLived},
		{"one_shot", "one_shot", agent.ModeOneShot},
		{"resume", "resume", agent.ModeResume},
		{"subagent", "subagent", agent.ModeSubagent},
		{"background", "background", agent.ModeBackground},
		{"unknown defaults to long_lived", "garbage_mode", agent.ModeLongLived},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boot := &fakeAgentBoot{}
			b := NewBroker(Dependencies{AgentBoot: boot})

			// Provider must resolve to a bootdir-layout provider (Phase 0
			// task 04) or DispatchRetry routes to the HTTP retry branch
			// instead of AgentBoot.
			ev := &FailureEvent{SessionID: "s", Provider: "claude", Mode: tc.mode}
			if _, err := b.DispatchRetry(context.Background(), ev); err != nil {
				t.Fatalf("DispatchRetry: %v", err)
			}
			if len(boot.gotOpts) != 1 {
				t.Fatalf("Boot calls: got %d, want 1", len(boot.gotOpts))
			}
			if boot.gotOpts[0].Mode != tc.want {
				t.Errorf("Mode: got %v, want %v", boot.gotOpts[0].Mode, tc.want)
			}
		})
	}
}

// TestDispatchRetryNilEvent — defensive guard against orchestration bugs.
func TestDispatchRetryNilEvent(t *testing.T) {
	b := NewBroker(Dependencies{AgentBoot: &fakeAgentBoot{}})
	_, err := b.DispatchRetry(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil event")
	}
	if !errors.Is(err, errNilFailureEvent) {
		t.Errorf("error = %v, want errNilFailureEvent", err)
	}
}

// TestDispatchRetryNoAgentBootWired — broker degrades to a clean
// error rather than panicking when AgentBoot was never wired.
// Production wiring always supplies it; tests that only need
// classification can leave it nil.
func TestDispatchRetryNoAgentBootWired(t *testing.T) {
	b := NewBroker(Dependencies{}) // no AgentBoot
	_, err := b.DispatchRetry(context.Background(), &FailureEvent{SessionID: "s", Provider: "claude"})
	if err == nil {
		t.Fatal("expected error for missing AgentBoot")
	}
	if !errors.Is(err, errNoAgentBootWired) {
		t.Errorf("error = %v, want errNoAgentBootWired", err)
	}
}

// TestDispatchRetryNoHTTPRetryWired — the HTTP-provider sibling of
// TestDispatchRetryNoAgentBootWired: a no-bootdir-layout provider with
// no HTTPRetry wired degrades to a clean error (not a panic, not a
// silent no-op), and AgentBoot is never touched.
func TestDispatchRetryNoHTTPRetryWired(t *testing.T) {
	boot := &fakeAgentBoot{}
	b := NewBroker(Dependencies{AgentBoot: boot}) // no HTTPRetry
	_, err := b.DispatchRetry(context.Background(), &FailureEvent{SessionID: "s", Provider: "anthropic"})
	if err == nil {
		t.Fatal("expected error for missing HTTPRetry")
	}
	if !errors.Is(err, errNoHTTPRetryWired) {
		t.Errorf("error = %v, want errNoHTTPRetryWired", err)
	}
	if len(boot.gotOpts) != 0 {
		t.Errorf("AgentBoot.Boot must never be called for a no-bootdir-layout provider, got %d calls", len(boot.gotOpts))
	}
}

// TestDispatchRetryStoreErrAborts — when Store.MarkRuntimeRelaunching
// fails, the broker must surface the error and skip the Boot call so
// callers can decide whether to escalate to Permanent.
func TestDispatchRetryStoreErrAborts(t *testing.T) {
	wantErr := errors.New("db locked")
	boot := &fakeAgentBoot{}
	store := &fakeStore{relaunchErr: wantErr}
	b := NewBroker(Dependencies{AgentBoot: boot, Store: store})

	_, err := b.DispatchRetry(context.Background(), &FailureEvent{SessionID: "s", Provider: "claude"})
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want wrapping %v", err, wantErr)
	}
	if len(boot.gotOpts) != 0 {
		t.Errorf("Boot should not have been called after Store err, got %d calls", len(boot.gotOpts))
	}
}

// TestDispatchRetryNilStoreOK — Store is optional. When nil, the
// broker skips the relaunch transition and proceeds to Boot. Used by
// tests + early integrations that don't have persistence wired.
func TestDispatchRetryNilStoreOK(t *testing.T) {
	boot := &fakeAgentBoot{}
	b := NewBroker(Dependencies{AgentBoot: boot}) // Store nil

	_, err := b.DispatchRetry(context.Background(), &FailureEvent{SessionID: "s", Provider: "claude"})
	if err != nil {
		t.Fatalf("DispatchRetry should succeed with nil Store, got %v", err)
	}
	if len(boot.gotOpts) != 1 {
		t.Errorf("Boot should still be called, got %d calls", len(boot.gotOpts))
	}
}

// TestDispatchRetryPropagatesBootErr — when AgentBoot.Boot fails, the
// broker surfaces the error so orchestration can escalate.
func TestDispatchRetryPropagatesBootErr(t *testing.T) {
	wantErr := errors.New("sandbox exec failed")
	boot := &fakeAgentBoot{retErr: wantErr}
	b := NewBroker(Dependencies{AgentBoot: boot})

	_, err := b.DispatchRetry(context.Background(), &FailureEvent{SessionID: "s", Provider: "claude"})
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want wrapping %v", err, wantErr)
	}
}

// TestReplacementSessionHook_FiredOnSuccessfulDispatch pins the chat-side
// adoption contract: when DispatchRetry succeeds (via the orchestration
// runTransientRetry path), the broker invokes the replacement-session
// hook with the chat sessionID and the booted *agent.Session. Without
// this hook the broker's replacement is orphaned and the next user
// turn boots yet another session (the bug Copilot flagged on PR #107).
func TestReplacementSessionHook_FiredOnSuccessfulDispatch(t *testing.T) {
	stub := &agent.Session{}
	boot := &fakeAgentBoot{retSess: stub}

	type call struct {
		sessionID string
		sess      *agent.Session
	}
	var got []call

	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     &fakeStore{},
		Envelope:  &nopEnvelope{},
	},
		WithReplacementSessionHook(func(sessionID string, s *agent.Session) {
			got = append(got, call{sessionID, s})
		}),
	)

	// Trigger an OnSessionExit that lands in runTransientRetry — the
	// idle_timeout cause maps to ClassTransient/RemediationNone.
	// Provider must resolve to a bootdir-layout provider so DispatchRetry
	// exercises the AgentBoot path under test.
	b.OnSessionExit("sess-replace", makeIdleTimeoutExit(), map[string]any{MetaKeyProvider: "claude"})

	if len(got) != 1 {
		t.Fatalf("replacement hook: got %d calls, want 1", len(got))
	}
	if got[0].sessionID != "sess-replace" {
		t.Errorf("hook sessionID = %q, want sess-replace", got[0].sessionID)
	}
	if got[0].sess != stub {
		t.Errorf("hook session = %p, want stub %p", got[0].sess, stub)
	}
}

// TestReplacementSessionHook_NotFiredOnDispatchFailure pins the negative
// case: when DispatchRetry returns an error the broker escalates to
// permanent and MUST NOT invoke the replacement hook (no real session
// exists to adopt).
func TestReplacementSessionHook_NotFiredOnDispatchFailure(t *testing.T) {
	bootErr := errors.New("boot failed")
	boot := &fakeAgentBoot{retErr: bootErr}

	called := 0
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     &fakeStore{},
		Envelope:  &nopEnvelope{},
	},
		WithReplacementSessionHook(func(string, *agent.Session) { called++ }),
	)

	b.OnSessionExit("sess-fail", makeIdleTimeoutExit(), map[string]any{MetaKeyProvider: "claude"})

	if called != 0 {
		t.Errorf("replacement hook should not fire on dispatch failure; got %d calls", called)
	}
}

// TestSetReplacementSessionHook_PostConstruction verifies the
// composition root path: the hook is installed AFTER NewBroker runs
// (because chatServiceImpl is constructed after agentDeps in the
// container) and still fires on the next OnSessionExit.
func TestSetReplacementSessionHook_PostConstruction(t *testing.T) {
	stub := &agent.Session{}
	boot := &fakeAgentBoot{retSess: stub}
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     &fakeStore{},
		Envelope:  &nopEnvelope{},
	})

	// Provider must resolve to a bootdir-layout provider throughout so
	// every OnSessionExit below exercises the AgentBoot success path
	// (DispatchRetry must succeed for notifyReplacement to ever fire).
	cliProviderMeta := map[string]any{MetaKeyProvider: "claude"}

	// No hook set yet: dispatch fires but no callback runs.
	called := 0
	b.OnSessionExit("sess-pre", makeIdleTimeoutExit(), cliProviderMeta)
	if called != 0 {
		t.Errorf("baseline (no hook): callback fired %d times", called)
	}

	// Install hook post-construction; subsequent dispatches invoke it.
	b.SetReplacementSessionHook(func(string, *agent.Session) { called++ })
	b.OnSessionExit("sess-post", makeIdleTimeoutExit(), cliProviderMeta)
	if called != 1 {
		t.Errorf("post-set hook: got %d calls, want 1", called)
	}

	// Clearing the hook (nil) restores the no-op behavior.
	b.SetReplacementSessionHook(nil)
	b.OnSessionExit("sess-cleared", makeIdleTimeoutExit(), cliProviderMeta)
	if called != 1 {
		t.Errorf("cleared hook: should still be %d calls, got %d", 1, called)
	}
}

// nopEnvelope satisfies recovery.EnvelopeSink for tests that don't care
// about envelope content — only the dispatch path.
type nopEnvelope struct{}

func (nopEnvelope) Emit(string, Envelope) error { return nil }

// makeIdleTimeoutExit is a helper for the replacement-hook tests above.
// idle_timeout cause maps to ClassTransient/RemediationNone, which
// routes through runTransientRetry — the dispatch-but-no-remediation
// path that exercises notifyReplacement directly.
func makeIdleTimeoutExit() *agentsessions.ExitError {
	return &agentsessions.ExitError{Cause: agentsessions.CauseIdleTimeout}
}

// TestItoa exercises the small int formatter used in the relaunch
// reason. Pinned because it's hand-rolled.
func TestItoa(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
		{-7, "-7"},
		{-12345, "-12345"},
	}
	for _, tc := range cases {
		if got := itoa(tc.in); got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
