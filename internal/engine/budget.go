package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// errTokenBudget is the cancellation cause of a subtask stopped because the
// turn's shared token budget ran out while it was running.
var errTokenBudget = errors.New("the turn's shared token budget is spent")

// errBudgetSpent refuses a foreground subtask on a spent shared budget.
var errBudgetSpent = errors.New("this turn has spent its shared token budget; the subtask did not start")

// turnBudget is one root turn's token ceiling, shared with every foreground
// subtask it starts, at any depth. Each turn adds its own model usage as it
// arrives, so a child's tokens are counted once, where they are spent, and
// never again when they roll up into the parent's usage. When the total
// reaches the limit every subtask still running is cancelled with
// errTokenBudget, so the budget holds mid-step, not only between steps.
type turnBudget struct {
	limit int64
	used  atomic.Int64

	mu      sync.Mutex
	next    int
	running map[int]context.CancelCauseFunc
}

type turnBudgetKey struct{}

// sharedBudget is the budget a turn draws on: a new one for a root turn when
// the agent sets a limit, the starting turn's for a foreground subtask (it
// rides on the child's run context), and none otherwise. A resumed root turn
// starts a new budget.
func sharedBudget(ctx context.Context, depth, limit int) *turnBudget {
	if depth > 0 {
		b, _ := ctx.Value(turnBudgetKey{}).(*turnBudget)
		return b
	}
	if limit <= 0 {
		return nil
	}
	return &turnBudget{limit: int64(limit), running: map[int]context.CancelCauseFunc{}}
}

// withBudget hands b to a subtask started with the returned context; a nil b
// clears any budget the context carried, as for a background subtask.
func withBudget(ctx context.Context, b *turnBudget) context.Context {
	return context.WithValue(ctx, turnBudgetKey{}, b)
}

// add counts tokens spent by one turn of the tree and, the moment the total
// reaches the limit, stops every subtask still running.
func (b *turnBudget) add(tokens int) {
	if b == nil || tokens <= 0 {
		return
	}
	total := b.used.Add(int64(tokens))
	if total < b.limit || total-int64(tokens) >= b.limit {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, cancel := range b.running {
		cancel(errTokenBudget)
	}
}

// spent reports whether the shared budget is used up.
func (b *turnBudget) spent() bool {
	return b != nil && b.used.Load() >= b.limit
}

// guard returns a context for a subtask's run that is cancelled with
// errTokenBudget when the budget runs out, and a func to call when the
// subtask ends.
func (b *turnBudget) guard(ctx context.Context) (context.Context, func()) {
	if b == nil {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancelCause(ctx)
	b.mu.Lock()
	id := b.next
	b.next++
	b.running[id] = cancel
	b.mu.Unlock()
	if b.spent() {
		cancel(errTokenBudget)
	}
	return ctx, func() {
		b.mu.Lock()
		delete(b.running, id)
		b.mu.Unlock()
		cancel(nil)
	}
}
