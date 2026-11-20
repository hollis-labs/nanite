// Package keepalive lets a provider stream say it is still busy while the
// model writes something the stream does not pass on as it arrives: a tool
// call's input, which is kept until the block ends, thinking, or pings.
//
// Without it, a turn's idle timer sees no event for as long as the model
// writes a long tool input (a whole document, say) and gives the turn up as
// stalled while the model is still writing. A stream that receives no frames
// at all sends no keepalives, so it still stalls.
package keepalive

import (
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// Event is the stream event a busy stream sends. Consumers ignore its type;
// a turn's idle timer counts it as activity. It carries nothing.
const Event llmtypes.EventType = "keepalive"

// DefaultEvery is how often, at most, a busy stream sends a keepalive.
const DefaultEvery = 5 * time.Second

var every atomic.Int64

func init() { every.Store(int64(DefaultEvery)) }

// SetEvery changes the keepalive interval and returns the previous one. It is
// for tests; a stream reads the interval on each frame.
func SetEvery(d time.Duration) time.Duration { return time.Duration(every.Swap(int64(d))) }

// Clock tracks when a stream last sent an event. It is used by the one
// goroutine that writes the stream.
type Clock struct{ last time.Time }

// Start returns a clock that counts from now.
func Start() *Clock { return &Clock{last: time.Now()} }

// Sent records that the stream just sent an event.
func (c *Clock) Sent() { c.last = time.Now() }

// Due reports whether a frame that arrived now should be answered with a
// keepalive: nothing has been sent for the keepalive interval.
func (c *Clock) Due() bool { return time.Since(c.last) >= time.Duration(every.Load()) }
