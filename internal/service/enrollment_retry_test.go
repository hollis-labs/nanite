package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tetherregistry"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func TestDeregisterBackoffGrowsAndCaps(t *testing.T) {
	want := []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 2 * time.Hour, 2 * time.Hour}
	for i, w := range want {
		if got := deregisterBackoff(i + 1); got != w {
			t.Errorf("failures=%d: %v, want %v", i+1, got, w)
		}
	}
	if deregisterBackoff(1000) != 2*time.Hour {
		t.Error("backoff overflowed its cap")
	}
}

// pendingEnv enrolls and retires one agent with Tether down, leaving a pending
// deregistration.
func pendingEnv(t *testing.T) *enrollEnv {
	t.Helper()
	ctx := context.Background()
	e := newEnrollEnv(t)
	if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); err != nil {
		t.Fatal(err)
	}
	e.fake.Drop()
	if out, err := e.svc.Retire(ctx, "c", e.host.ID, "r"); err != nil || !out.DeregisterPending {
		t.Fatalf("retire: %+v %v", out, err)
	}
	return e
}

func TestRetrierRetriesAfterTetherComesBackWithBackoff(t *testing.T) {
	ctx := context.Background()
	e := pendingEnv(t)
	clk := &fakeClock{t: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)}
	r := &DeregisterRetrier{Svc: e.svc, Now: clk.Now}
	_, _, base := e.fake.Counts()

	if done, failed := r.Pass(ctx); done != 0 || failed != 1 { // attempt 1 fails: next in 15m
		t.Fatalf("pass 1 = %d/%d", done, failed)
	}
	attempts := func() int { _, _, d := e.fake.Counts(); return d - base }
	// Tether is dropping connections, which the fake counts only when it answers;
	// count attempts through the record instead.
	rec := func() store.AgentEnrollment {
		x, _ := e.st.ActorEnrollment().Get(ctx, e.host.ID)
		return x
	}
	a1 := rec().Attempts
	clk.Advance(14 * time.Minute)
	if done, failed := r.Pass(ctx); done+failed != 0 || rec().Attempts != a1 {
		t.Fatal("retried before the backoff elapsed")
	}
	clk.Advance(time.Minute) // 15m: second attempt, fails, next in 30m
	if _, failed := r.Pass(ctx); failed != 1 || rec().Attempts != a1+1 {
		t.Fatal("did not retry at 15m")
	}
	clk.Advance(29 * time.Minute)
	if done, failed := r.Pass(ctx); done+failed != 0 {
		t.Fatal("retried before the doubled backoff")
	}
	e.fake.Heal()
	clk.Advance(time.Minute) // 30m after the second failure
	if done, failed := r.Pass(ctx); done != 1 || failed != 0 {
		t.Fatalf("recovery pass = %d/%d", done, failed)
	}
	if rec().Status != store.EnrollmentRetired || attempts() != 1 {
		t.Fatalf("record = %+v, deregister calls = %d", rec(), attempts())
	}
	if es := e.fake.Entries(); es[0].Status != "deprecated" {
		t.Fatalf("entry = %+v", es[0])
	}
	if done, failed := r.Pass(ctx); done+failed != 0 {
		t.Fatal("a finished record was retried")
	}
}

func TestRetrierBackoffIsCappedPerRecord(t *testing.T) {
	ctx := context.Background()
	e := pendingEnv(t)
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	r := &DeregisterRetrier{Svc: e.svc, Now: clk.Now}
	for i := 0; i < 8; i++ {
		r.Pass(ctx)
		clk.Advance(2 * time.Hour)
	}
	r.mu.Lock()
	st := r.state[e.host.ID]
	r.mu.Unlock()
	if st == nil || st.failures != 8 {
		t.Fatalf("state = %+v", st)
	}
	if got := st.next.Sub(clk.Now().Add(-2 * time.Hour)); got != 2*time.Hour {
		t.Fatalf("wait after many failures = %v, want the 2h cap", got)
	}
}

func TestRetrierPassIsBounded(t *testing.T) {
	ctx := context.Background()
	e := pendingEnv(t)
	for _, slug := range []string{"pend-b", "pend-c"} {
		o := newEnrollEnvOn(t, e.st, slug)
		o.svc.Registrar = e.svc.Registrar
		e.fake.Heal()
		if _, err := o.svc.Enroll(ctx, "c", o.host.ID, "r", ""); err != nil {
			t.Fatal(err)
		}
		e.fake.Drop()
		if _, err := o.svc.Retire(ctx, "c", o.host.ID, "r"); err != nil {
			t.Fatal(err)
		}
	}
	e.fake.Heal()
	r := &DeregisterRetrier{Svc: e.svc, Batch: 2}
	if done, failed := r.Pass(ctx); done != 2 || failed != 0 {
		t.Fatalf("pass = %d/%d, want 2 (the batch bound)", done, failed)
	}
	if done, _ := r.Pass(ctx); done != 1 {
		t.Fatalf("second pass = %d, want the remaining 1", done)
	}
}

func TestRetrierDoesNothingWithoutAUsableRegistryToken(t *testing.T) {
	ctx := context.Background()
	e := pendingEnv(t)
	e.fake.Heal()
	r := &DeregisterRetrier{Svc: &EnrollmentService{Store: e.st, Registrar: e.fake.Registrar(filepath.Join(t.TempDir(), "absent"))}}
	reg, look, dereg := e.fake.Counts()
	if done, failed := r.Pass(ctx); done+failed != 0 {
		t.Fatalf("pass = %d/%d", done, failed)
	}
	r.Pass(ctx)
	if a, b, c := e.fake.Counts(); a != reg || b != look || c != dereg {
		t.Fatal("Tether was called without a token")
	}
	loose := filepath.Join(t.TempDir(), "loose")
	_ = os.WriteFile(loose, []byte("x"), 0o644)
	_ = os.Chmod(loose, 0o644)
	r.Svc.Registrar = e.fake.Registrar(loose)
	if done, failed := r.Pass(ctx); done+failed != 0 {
		t.Fatal("an insecure token file was used")
	}
	if rec, _ := e.st.ActorEnrollment().Get(ctx, e.host.ID); rec.Status != store.EnrollmentDeregisterPending {
		t.Fatal("record changed")
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRetryLoopRunsAtStartupAndOnTicksAndStopsOnShutdown(t *testing.T) {
	e := pendingEnv(t)
	e.fake.Heal()
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := e.svc.StartDeregisterRetry(ctx, RetryLoopOptions{Ticks: ticks, Now: clk.Now})
	if stop == nil {
		t.Fatal("loop not started although Tether is configured")
	}
	// The startup pass finishes the pending record without any tick.
	waitFor(t, "startup pass", func() bool {
		rec, _ := e.st.ActorEnrollment().Get(context.Background(), e.host.ID)
		return rec.Status == store.EnrollmentRetired
	})

	// A later record is picked up by a tick.
	o := newEnrollEnvOn(t, e.st, "loop-b")
	o.svc.Registrar = e.svc.Registrar
	if _, err := o.svc.Enroll(context.Background(), "c", o.host.ID, "r", ""); err != nil {
		t.Fatal(err)
	}
	e.fake.Drop()
	if _, err := o.svc.Retire(context.Background(), "c", o.host.ID, "r"); err != nil {
		t.Fatal(err)
	}
	e.fake.Heal()
	clk.Advance(time.Hour)
	ticks <- clk.Now()
	waitFor(t, "tick pass", func() bool {
		rec, _ := e.st.ActorEnrollment().Get(context.Background(), o.host.ID)
		return rec.Status == store.EnrollmentRetired
	})

	// Shutdown: stop returns, and further ticks are not consumed.
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return")
	}
	select {
	case ticks <- clk.Now():
		t.Fatal("the loop still reads ticks after shutdown")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRetryLoopStopsWhenItsContextIsCancelled(t *testing.T) {
	e := newEnrollEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	stop := e.svc.StartDeregisterRetry(ctx, RetryLoopOptions{Ticks: make(chan time.Time)})
	cancel()
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not exit on context cancel")
	}
}

func TestRetryLoopIsNotStartedWhenTetherIsNotConfigured(t *testing.T) {
	e := newEnrollEnv(t)
	e.svc.Registrar = &tetherregistry.Client{Config: func() tetherregistry.Config { return tetherregistry.Config{} }}
	if stop := e.svc.StartDeregisterRetry(context.Background(), RetryLoopOptions{}); stop != nil {
		stop()
		t.Fatal("started without a Tether address")
	}
}
