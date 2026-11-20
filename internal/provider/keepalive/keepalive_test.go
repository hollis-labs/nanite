package keepalive

import (
	"testing"
	"time"
)

func TestClockIsDueOnlyAfterEvery(t *testing.T) {
	old := SetEvery(30 * time.Millisecond)
	t.Cleanup(func() { SetEvery(old) })
	c := Start()
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
