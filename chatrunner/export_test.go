package chatrunner

import "time"

// SetPublisherIdle sets how long a notice publisher waits before it stops,
// and returns a function that restores it (tests only).
func SetPublisherIdle(d time.Duration) func() {
	old := publisherIdle
	publisherIdle = d
	return func() { publisherIdle = old }
}
