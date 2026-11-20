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
	"context"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// Event is the stream event a busy stream sends. Consumers ignore its type;
// a turn's idle timer counts it as activity. It carries nothing.
const Event llmtypes.EventType = "keepalive"

// DefaultEvery is how often, at most, a busy stream sends a keepalive.
const DefaultEvery = 5 * time.Second

// MinEvery is the shortest interval derived from an idle timeout.
const MinEvery = 100 * time.Millisecond

var every atomic.Int64

func init() { every.Store(int64(DefaultEvery)) }

// SetEvery changes the default keepalive interval and returns the previous
// one. It is for tests; a stream reads it when it starts.
func SetEvery(d time.Duration) time.Duration { return time.Duration(every.Swap(int64(d))) }

type idleKey struct{}

// WithIdle tells the provider streams started with ctx the idle timeout their
// reader uses, so they send keepalives often enough for it.
func WithIdle(ctx context.Context, idle time.Duration) context.Context {
	return context.WithValue(ctx, idleKey{}, idle)
}

// Interval is how often, at most, a stream started with ctx sends a
// keepalive: the default interval, or a third of the reader's idle timeout
// when that is shorter (never below MinEvery), so a short idle timeout still
// sees at least two keepalives before it fires.
func Interval(ctx context.Context) time.Duration {
	d := time.Duration(every.Load())
	if idle, ok := ctx.Value(idleKey{}).(time.Duration); ok && idle > 0 {
		if third := max(idle/3, MinEvery); third < d {
			d = third
		}
	}
	return d
}

// Clock tracks when a stream last sent an event. It is used by the one
// goroutine that writes the stream.
type Clock struct {
	last  time.Time
	every time.Duration
}

// Start returns a clock that counts from now, at the interval for ctx.
func Start(ctx context.Context) *Clock { return &Clock{last: time.Now(), every: Interval(ctx)} }

// Sent records that the stream just sent an event.
func (c *Clock) Sent() { c.last = time.Now() }

// Due reports whether a frame that arrived now should be answered with a
// keepalive: nothing has been sent for the keepalive interval.
func (c *Clock) Due() bool { return time.Since(c.last) >= c.every }
