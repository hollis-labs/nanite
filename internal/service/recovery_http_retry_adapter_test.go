package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/nanite/internal/runtime/agent/recovery"
)

// TestRecoveryHTTPRetryAdapter_ForwardsToRetryLastMessage verifies the
// happy path: Retry forwards ev.SessionID into the bound
// retryLastMessage function and returns its error verbatim (nil here).
func TestRecoveryHTTPRetryAdapter_ForwardsToRetryLastMessage(t *testing.T) {
	var gotCtx context.Context
	var gotSessionID string
	a := newRecoveryHTTPRetryAdapter(func(ctx context.Context, sessionID string) (string, error) {
		gotCtx = ctx
		gotSessionID = sessionID
		return "new-assistant-msg-id", nil
	})

	ctx := context.Background()
	ev := &recovery.FailureEvent{SessionID: "sess-abc", Provider: "anthropic"}
	if err := a.Retry(ctx, ev); err != nil {
		t.Fatalf("Retry: unexpected err = %v", err)
	}
	if gotSessionID != "sess-abc" {
		t.Errorf("retryLastMessage sessionID = %q, want sess-abc", gotSessionID)
	}
	if gotCtx != ctx {
		t.Errorf("retryLastMessage did not receive the same ctx")
	}
}

// TestRecoveryHTTPRetryAdapter_PropagatesUnderlyingErr verifies a
// RetryLastMessage failure (e.g. "no user message found in session")
// surfaces to the broker so DispatchRetry/orchestration can escalate.
func TestRecoveryHTTPRetryAdapter_PropagatesUnderlyingErr(t *testing.T) {
	wantErr := errors.New("no user message found in session sess-abc")
	a := newRecoveryHTTPRetryAdapter(func(context.Context, string) (string, error) {
		return "", wantErr
	})

	err := a.Retry(context.Background(), &recovery.FailureEvent{SessionID: "sess-abc"})
	if !errors.Is(err, wantErr) {
		t.Errorf("Retry err = %v, want %v", err, wantErr)
	}
}

// TestRecoveryHTTPRetryAdapter_NilGuards verifies the adapter degrades
// cleanly (error, not panic) for a nil adapter, an unwired retry func,
// a nil event, and an empty SessionID.
func TestRecoveryHTTPRetryAdapter_NilGuards(t *testing.T) {
	t.Run("nil adapter", func(t *testing.T) {
		var a *recoveryHTTPRetryAdapter
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Retry panicked on nil adapter: %v", r)
			}
		}()
		if err := a.Retry(context.Background(), &recovery.FailureEvent{SessionID: "s"}); err == nil {
			t.Fatal("expected error from nil adapter")
		}
	})

	t.Run("unwired retry func", func(t *testing.T) {
		a := &recoveryHTTPRetryAdapter{}
		if err := a.Retry(context.Background(), &recovery.FailureEvent{SessionID: "s"}); err == nil {
			t.Fatal("expected error from unwired retryLastMessage")
		}
	})

	t.Run("nil event", func(t *testing.T) {
		called := false
		a := newRecoveryHTTPRetryAdapter(func(context.Context, string) (string, error) {
			called = true
			return "", nil
		})
		if err := a.Retry(context.Background(), nil); err == nil {
			t.Fatal("expected error for nil event")
		}
		if called {
			t.Error("retryLastMessage should not be called for a nil event")
		}
	})

	t.Run("empty session id", func(t *testing.T) {
		called := false
		a := newRecoveryHTTPRetryAdapter(func(context.Context, string) (string, error) {
			called = true
			return "", nil
		})
		if err := a.Retry(context.Background(), &recovery.FailureEvent{SessionID: ""}); err == nil {
			t.Fatal("expected error for empty session id")
		}
		if called {
			t.Error("retryLastMessage should not be called for an empty session id")
		}
	})
}

// Compile-time assertion that the adapter satisfies recovery.HTTPRetry.
var _ recovery.HTTPRetry = (*recoveryHTTPRetryAdapter)(nil)

// smokeRecoveryStore is a minimal recovery.BrokerStore fake for the
// end-to-end smoke test below — only WriteBreadcrumb matters here.
type smokeRecoveryStore struct {
	breadcrumbs []recovery.Breadcrumb
}

func (s *smokeRecoveryStore) MarkRuntimeRelaunching(string, string) error { return nil }

func (s *smokeRecoveryStore) WriteBreadcrumb(b recovery.Breadcrumb) error {
	s.breadcrumbs = append(s.breadcrumbs, b)
	return nil
}

// TestSmoke_BrokerHTTPRetryThroughAdapter is the end-to-end functional
// check for Phase 0 task 04 / decision log §19, built the same way this
// package's existing recovery-adapter smoke tests are (see
// recovery_credentials_smoke_test.go): a real recovery.Broker wired
// against the REAL recoveryHTTPRetryAdapter (not a fakeHTTPRetry stand-in
// like the recovery package's own unit tests use), backed by a scripted
// "test double provider" retryLastMessage func standing in for
// chatServiceImpl.RetryLastMessage.
//
// Drives the exact shape notifyRecoveryBrokerForHTTPStreamError produces
// for a real HTTP-provider (anthropic) transient stream error — Code -1,
// a synthesized cause, no lib-level Cause — through OnSessionExit and
// confirms: the scripted retry fires with the right sessionID, no
// AgentBoot-shaped call ever happens (there's no AgentBoot wired at
// all — if the branch were wrong this would panic or error instead of
// reaching the retry func), and a breadcrumb lands with
// OutcomeTransientRetrySucceeded.
func TestSmoke_BrokerHTTPRetryThroughAdapter(t *testing.T) {
	var retryCalls int32
	var gotSessionID atomic.Value
	testDoubleRetryLastMessage := func(ctx context.Context, sessionID string) (string, error) {
		atomic.AddInt32(&retryCalls, 1)
		gotSessionID.Store(sessionID)
		return "new-assistant-msg-id", nil // mirrors a successful dispatch
	}

	store := &smokeRecoveryStore{}
	b := recovery.NewBroker(recovery.Dependencies{
		Store: store,
		// AgentBoot intentionally left nil — proves the HTTP-provider
		// branch never reaches it for a no-bootdir-layout provider.
	}, recovery.WithRemediationTimeout(20*time.Millisecond)) // keep backoff fast

	b.SetHTTPRetry(newRecoveryHTTPRetryAdapter(testDoubleRetryLastMessage))

	// Mirrors chat_http_broker_notify.go's synthesized ExitError for an
	// HTTP-stream timeout (causeHTTPStreamTimeout would be the real
	// value; the literal string here is enough to prove the shape).
	exit := &agentsessions.ExitError{Code: -1, Cause: "http_stream_timeout"}
	b.OnSessionExit("smoke-http-session", exit, map[string]any{
		recovery.MetaKeyProvider:     "anthropic",
		recovery.MetaKeyAgentProfile: "claude-sonnet",
		recovery.MetaKeyMode:         "one_shot",
	})

	if got := atomic.LoadInt32(&retryCalls); got != 1 {
		t.Fatalf("test-double retryLastMessage calls: got %d, want 1", got)
	}
	if got, _ := gotSessionID.Load().(string); got != "smoke-http-session" {
		t.Errorf("retryLastMessage sessionID = %q, want smoke-http-session", got)
	}

	if len(store.breadcrumbs) != 1 {
		t.Fatalf("breadcrumbs: got %d, want 1", len(store.breadcrumbs))
	}
	bc := store.breadcrumbs[0]
	if bc.Outcome != recovery.OutcomeTransientRetrySucceeded {
		t.Errorf("breadcrumb Outcome = %v, want OutcomeTransientRetrySucceeded", bc.Outcome)
	}
	if bc.Cause != "http_stream_timeout" {
		t.Errorf("breadcrumb Cause = %q, want http_stream_timeout", bc.Cause)
	}
}
