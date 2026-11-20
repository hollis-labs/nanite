package engine

import (
	"context"
	"sync"

	"github.com/hollis-labs/nanite/internal/events"
)

// testNotices holds a notice subscription per test engine, made by
// setupEngine as soon as the engine exists, so a test sees every notice.
var testNotices sync.Map

func subscribeForTest(e *Engine) {
	testNotices.Store(e, e.SubscribeNotices(context.Background()))
}

// Notices is every notice the engine has posted, in order (tests only).
func (e *Engine) Notices() <-chan events.NoticePosted {
	ch, _ := testNotices.Load(e)
	return ch.(<-chan events.NoticePosted)
}
