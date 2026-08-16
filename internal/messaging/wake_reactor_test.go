package messaging

// Tests for CW-20260816-0065: SendMessage's wake-reactor wiring. Proves
// the insertion point (SendMessage, after writeSendEvents) fires the
// wired WakeReactor exactly once per eligible send, and — the
// double-wake guard the ticket calls out explicitly — never for
// Kind=KindSubagentResult, which already has its own dedicated wake
// path (subagent.CompletionReactor, wired at the subagent service's own
// SendMessage call site in internal/subagent/service.go).

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeWakeReactor records ReactToMessage calls so tests can assert
// whether SendMessage triggered the live-wake side effect. SendMessage
// fires the reactor in its own goroutine (see the SendMessage call
// site's comment), so callers must poll rather than assert immediately.
type fakeWakeReactor struct {
	mu    sync.Mutex
	calls []*Message
}

func (f *fakeWakeReactor) ReactToMessage(_ context.Context, msg *Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, msg)
}

func (f *fakeWakeReactor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeWakeReactor) waitForCount(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f.count() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.count(); got != want {
		t.Fatalf("wake reactor call count = %d, want %d", got, want)
	}
}

func TestService_SendMessage_WakesReactorForOrdinaryMessage(t *testing.T) {
	svc, _, _ := newTestService(t, "file-backend")
	wake := &fakeWakeReactor{}
	svc.SetWakeReactor(wake)

	in := baseInput(UserSentinel, "file-backend")
	in.Kind = KindNotification
	out, err := svc.SendMessage(context.Background(), in)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	wake.waitForCount(t, 1)
	if wake.calls[0].ID != out.ID {
		t.Errorf("reactor received message id %q, want %q", wake.calls[0].ID, out.ID)
	}
}

// TestService_SendMessage_SkipsWakeForSubagentResult is the double-wake
// guard: kind=subagent_result already has its own dedicated wake path
// (subagent.CompletionReactor). If SendMessage's generic WakeReactor
// also fired for this kind, a subagent completion would race two
// different synthetic-prompt turn triggers against
// registerGenerationIfIdle.
func TestService_SendMessage_SkipsWakeForSubagentResult(t *testing.T) {
	svc, _, _ := newTestService(t, "file-backend")
	wake := &fakeWakeReactor{}
	svc.SetWakeReactor(wake)

	in := baseInput(UserSentinel, "file-backend")
	in.Kind = KindSubagentResult
	if _, err := svc.SendMessage(context.Background(), in); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// Send a second, ordinary message and wait for ITS wake call — this
	// gives the (absent) subagent_result wake call every opportunity to
	// have already landed if the exclusion were broken, without a fixed
	// sleep.
	in2 := baseInput(UserSentinel, "file-backend")
	in2.Kind = KindNotification
	if _, err := svc.SendMessage(context.Background(), in2); err != nil {
		t.Fatalf("SendMessage (control): %v", err)
	}
	wake.waitForCount(t, 1)

	if wake.count() != 1 {
		t.Fatalf("expected exactly 1 wake call (the ordinary message only), got %d", wake.count())
	}
	if wake.calls[0].Kind != KindNotification {
		t.Errorf("the single wake call should be for the ordinary message, got kind=%q", wake.calls[0].Kind)
	}
}

func TestService_SendMessage_NilWakeReactor_NoOp(t *testing.T) {
	// No SetWakeReactor call — svc.wake stays nil. Regression guard: a
	// send must not panic or behave differently when no reactor is wired
	// (the pre-CW-20260816-0065 / test-double-without-wiring shape).
	svc, _, _ := newTestService(t, "file-backend")

	if _, err := svc.SendMessage(context.Background(), baseInput(UserSentinel, "file-backend")); err != nil {
		t.Fatalf("SendMessage with nil wake reactor: %v", err)
	}
}
