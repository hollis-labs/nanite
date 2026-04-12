package safego

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// capturingHook records panic recoveries for assertions.
type capture struct {
	mu     sync.Mutex
	labels []string
	values []any
	stacks [][]byte
}

func (c *capture) hook() PanicHook {
	return func(label string, v any, stack []byte) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.labels = append(c.labels, label)
		c.values = append(c.values, v)
		c.stacks = append(c.stacks, stack)
	}
}

func (c *capture) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.labels)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within timeout")
}

func TestGo_NoPanicRunsFn(t *testing.T) {
	c := &capture{}
	defer SetPanicHook(SetPanicHook(c.hook()))

	var ran atomic.Bool
	done := make(chan struct{})
	Go(context.Background(), "test.nopanic", func() {
		ran.Store(true)
		close(done)
	})
	<-done
	if !ran.Load() {
		t.Fatal("fn did not run")
	}
	if got := c.len(); got != 0 {
		t.Fatalf("hook fired unexpectedly: %d times", got)
	}
}

func TestGo_RecoversPanic(t *testing.T) {
	cases := []struct {
		name  string
		panic any
		want  string
	}{
		{"string panic", "boom", "boom"},
		{"error panic", errors.New("oops"), "oops"},
		{"int panic", 42, "42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &capture{}
			defer SetPanicHook(SetPanicHook(c.hook()))

			Go(context.Background(), tc.name, func() {
				panic(tc.panic)
			})
			waitFor(t, func() bool { return c.len() == 1 })

			c.mu.Lock()
			defer c.mu.Unlock()
			if c.labels[0] != tc.name {
				t.Errorf("label = %q, want %q", c.labels[0], tc.name)
			}
			if len(c.stacks[0]) == 0 {
				t.Error("stack empty")
			}
		})
	}
}

func TestCall_NoPanicRunsFn(t *testing.T) {
	c := &capture{}
	defer SetPanicHook(SetPanicHook(c.hook()))

	var ran bool
	Call(context.Background(), "test.call.nopanic", func() { ran = true })
	if !ran {
		t.Fatal("fn did not run")
	}
	if got := c.len(); got != 0 {
		t.Fatalf("hook fired unexpectedly: %d", got)
	}
}

func TestCall_RecoversPanicSynchronous(t *testing.T) {
	c := &capture{}
	defer SetPanicHook(SetPanicHook(c.hook()))

	Call(context.Background(), "test.call.panic", func() {
		panic("sync-boom")
	})
	if got := c.len(); got != 1 {
		t.Fatalf("hook fired %d times, want 1", got)
	}
}

func TestSetPanicHook_NilRestoresNoOp(t *testing.T) {
	// Install a hook, then nil-restore.
	c := &capture{}
	prev := SetPanicHook(c.hook())
	defer SetPanicHook(prev)

	SetPanicHook(nil) // should be no-op, not panic

	Call(context.Background(), "nilhook", func() { panic("x") })
	if got := c.len(); got != 0 {
		t.Fatalf("hook fired after being cleared: %d", got)
	}
}

func TestGo_ContextNoSpanDoesNotPanic(t *testing.T) {
	// Background context has no active span. Code must not panic; the
	// tracer should create a standalone span for the event.
	c := &capture{}
	defer SetPanicHook(SetPanicHook(c.hook()))

	Go(context.Background(), "no-span-ctx", func() { panic("nospan") })
	waitFor(t, func() bool { return c.len() == 1 })
}
