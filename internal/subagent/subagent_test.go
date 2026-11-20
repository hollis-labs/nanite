package subagent

import (
	"strings"
	"testing"
)

func TestMaximumFourChildren(t *testing.T) {
	limit := NewLimit(0)
	var releases []func()
	for range 4 {
		release, err := limit.Acquire()
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	_, err := limit.Acquire()
	if err == nil {
		t.Fatal("allowed a fifth child")
	}
	if !strings.Contains(err.Error(), "4 subtasks are already running") || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("unclear refusal: %v", err)
	}
	releases[0]()
	releases[0]()
	if release, err := limit.Acquire(); err != nil {
		t.Fatal(err)
	} else {
		defer release()
	}
	if _, err := limit.Acquire(); err == nil {
		t.Fatal("double release increased available capacity")
	}
	for _, release := range releases[1:] {
		release()
	}
}

func TestLimitIsConfigurableUpToEight(t *testing.T) {
	for _, tc := range []struct{ set, want int }{{0, 4}, {-1, 4}, {2, 2}, {8, 8}, {20, 8}} {
		if got := cap(NewLimit(tc.set)); got != tc.want {
			t.Errorf("NewLimit(%d) holds %d, want %d", tc.set, got, tc.want)
		}
	}
}
