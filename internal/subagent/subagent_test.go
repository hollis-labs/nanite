package subagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLimitWaitsForASlot(t *testing.T) {
	limit := NewLimit(0)
	var releases []func()
	for range 4 {
		release, err := limit.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	got := make(chan error, 1)
	go func() {
		release, err := limit.Acquire(t.Context())
		if err == nil {
			release()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("a fifth subtask did not wait: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releases[0]()
	releases[0]()
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	if _, err := limit.TryAcquire(); err != nil {
		t.Fatalf("the freed slot was not returned: %v", err)
	}
	if _, err := limit.TryAcquire(); err == nil {
		t.Fatal("double release increased available capacity")
	}
	for _, release := range releases[1:] {
		release()
	}
}

func TestWaitingEndsWithItsContext(t *testing.T) {
	limit := NewLimit(1)
	hold, _ := limit.TryAcquire()
	defer hold()
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("turn ended")
	cancel(cause)
	if _, err := limit.Acquire(ctx); !errors.Is(err, cause) {
		t.Fatalf("waiting did not end with the context's cause: %v", err)
	}
}

func TestTryAcquireRefusesClearly(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{1, "1 subtask is already running"}, {4, "4 subtasks are already running"}} {
		limit := NewLimit(tc.n)
		for range tc.n {
			if _, err := limit.TryAcquire(); err != nil {
				t.Fatal(err)
			}
		}
		_, err := limit.TryAcquire()
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "did not start") {
			t.Fatalf("limit %d refused with %v", tc.n, err)
		}
	}
}

func TestLimitIsConfigurableUpToEight(t *testing.T) {
	for _, tc := range []struct{ set, want int }{{0, 4}, {-1, 4}, {2, 2}, {8, 8}, {20, 8}} {
		if got := cap(NewLimit(tc.set)); got != tc.want {
			t.Errorf("NewLimit(%d) holds %d, want %d", tc.set, got, tc.want)
		}
	}
}
