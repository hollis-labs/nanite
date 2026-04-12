package plugin

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
)

// panickingFilterFn returns a plugin FilterFunc that panics when invoked.
func panickingFilterFn(panicMsg string) FilterFunc {
	return func(data interface{}, ctx FilterContext) (interface{}, error) {
		panic(panicMsg)
	}
}

// TestPluginHookPanic_RecoveredBySafeCall verifies that when a plugin-supplied
// filter panics inside safego.Call (as wired in FilterRegistry.Apply), the
// process does not crash, the panic hook fires with the expected label and
// value, and the test continues to completion.
//
// This is the TASK-013 synthetic panic test asserting the three properties:
//
//	(a) no process crash
//	(b) slog.Error fires with the panic value
//	(c) test continues normally
//
// slog.Error is observed indirectly via the SetPanicHook callback — every
// recover path in safego emits slog.Error before invoking the hook, so a
// hook call proves slog.Error fired.
func TestPluginHookPanic_RecoveredBySafeCall(t *testing.T) {
	var (
		hookFired atomic.Bool
		gotLabel  string
		gotValue  any
		mu        sync.Mutex
	)
	prev := safego.SetPanicHook(func(label string, v any, stack []byte) {
		mu.Lock()
		defer mu.Unlock()
		hookFired.Store(true)
		gotLabel = label
		gotValue = v
	})
	defer safego.SetPanicHook(prev)

	reg := NewFilterRegistry()
	_ = reg.Register("user_message", "test.panicky", 100, panickingFilterFn("intentional plugin panic"))

	// (a) No process crash: Apply must return without panicking.
	_, err := reg.Apply("user_message", "hello", FilterContext{
		SessionID: "s-test",
		AgentID:   "a-test",
	})

	// The filter function panicked and never returned a (result, err) pair.
	// Apply observes the zero-valued locals after safego.Call and returns
	// (nil, nil). Asserting err == nil confirms we took the recovery path
	// rather than bubbling up a real error.
	if err != nil {
		t.Fatalf("Apply returned error = %v, want nil (panic should be swallowed by safego.Call)", err)
	}

	// (b) Panic hook fired with the expected label + value. Hook fires
	// synchronously inside safego.Call so no wait loop is required, but keep
	// a small deadline for safety against scheduler jitter.
	deadline := time.Now().Add(500 * time.Millisecond)
	for !hookFired.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !hookFired.Load() {
		t.Fatal("panic hook did not fire")
	}

	mu.Lock()
	defer mu.Unlock()
	wantLabelPrefix := "plugin-hook.filter-apply."
	if len(gotLabel) < len(wantLabelPrefix) || gotLabel[:len(wantLabelPrefix)] != wantLabelPrefix {
		t.Errorf("panic label = %q, want prefix %q", gotLabel, wantLabelPrefix)
	}
	if s, ok := gotValue.(string); !ok || s != "intentional plugin panic" {
		t.Errorf("panic value = %v (%T), want string \"intentional plugin panic\"", gotValue, gotValue)
	}

	// (c) Test continues normally past this point.
	if ctx := context.Background(); ctx == nil {
		t.Fatal("unreachable: context.Background returned nil")
	}
}
