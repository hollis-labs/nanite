package subagent

import "testing"

func TestMaximumFourChildren(t *testing.T) {
	limit := NewLimit()
	var releases []func()
	for range 4 {
		release, err := limit.Acquire()
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := limit.Acquire(); err == nil {
		t.Fatal("allowed a fifth child")
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
