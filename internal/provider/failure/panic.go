package failure

import (
	"context"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// fallbackGuard is used by an adapter that was given no guard. It withholds
// panic values, which it cannot redact, and reports to standard error.
var fallbackGuard = panics.New(panics.Config{})

// Guard returns g, or a guard that withholds panic values when g is nil.
func Guard(g *panics.Guard) *panics.Guard {
	if g == nil {
		return fallbackGuard
	}
	return g
}

// RecoverStream is deferred by a provider stream goroutine, after the
// deferred close of its channel so that it runs first. A panic becomes a
// fatal EventError carrying only the panic ref, so the turn fails instead of
// the host process.
func RecoverStream(ctx context.Context, g *panics.Guard, where string, out chan<- llmtypes.StreamEvent) {
	v := recover()
	if v == nil {
		return
	}
	pe := Guard(g).Capture(where, v)
	select {
	case out <- Event(&Error{Class: Fatal, Cause: pe}):
	case <-ctx.Done():
	}
}
