package chatrunner

import "time"

// SetPublisherIdle sets how long a notice publisher waits before it stops,
// and returns a function that restores it (tests only).
func SetPublisherIdle(d time.Duration) func() {
	old := publisherIdle
	publisherIdle = d
	return func() { publisherIdle = old }
}

// AuthClock drives the grant-request watchers by hand (tests only): Tick
// fires every pending wait (poll and stagger) once, and Advance moves the
// clock the max-age check reads.
type AuthClock struct {
	ticks chan time.Time
	mu    chan struct{}
	now   time.Time
}

// UseAuthClock replaces the watchers' clock until the test ends.
func UseAuthClock(t interface{ Cleanup(func()) }) *AuthClock {
	c := &AuthClock{ticks: make(chan time.Time), mu: make(chan struct{}, 1), now: time.Now()}
	c.mu <- struct{}{}
	oldAfter, oldNow := authAfter, authNow
	authAfter = func(time.Duration) <-chan time.Time { return c.ticks }
	authNow = func() time.Time {
		<-c.mu
		defer func() { c.mu <- struct{}{} }()
		return c.now
	}
	t.Cleanup(func() { authAfter, authNow = oldAfter, oldNow })
	return c
}

// Tick wakes one waiting watcher, reporting false when none waited within
// five seconds.
func (c *AuthClock) Tick() bool {
	select {
	case c.ticks <- time.Now():
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// Advance moves the clock forward by d.
func (c *AuthClock) Advance(d time.Duration) {
	<-c.mu
	c.now = c.now.Add(d)
	c.mu <- struct{}{}
}
