package keepalive

import (
	"context"
	"testing"
	"time"
)

func TestClockIsDueOnlyAfterTheInterval(t *testing.T) {
	old := SetEvery(30 * time.Millisecond)
	t.Cleanup(func() { SetEvery(old) })
	c := Start(context.Background())
	if c.Due() {
		t.Fatal("due right after start")
	}
	time.Sleep(40 * time.Millisecond)
	if !c.Due() {
		t.Fatal("not due after the interval")
	}
	c.Sent()
	if c.Due() {
		t.Fatal("due right after a send")
	}
}

// The interval is the default, or a third of the reader's idle timeout when
// that is shorter, never below MinEvery.
func TestIntervalFollowsTheIdleTimeout(t *testing.T) {
	for _, tc := range []struct{ idle, want time.Duration }{
		{0, DefaultEvery},
		{90 * time.Second, DefaultEvery},
		{15 * time.Second, DefaultEvery},
		{3 * time.Second, time.Second},
		{150 * time.Millisecond, MinEvery},
	} {
		ctx := context.Background()
		if tc.idle > 0 {
			ctx = WithIdle(ctx, tc.idle)
		}
		if got := Interval(ctx); got != tc.want {
			t.Errorf("idle %s: interval %s, want %s", tc.idle, got, tc.want)
		}
	}
}
