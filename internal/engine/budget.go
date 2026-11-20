package engine

import (
	"context"
	"sync/atomic"
)

// turnBudget is one root turn's token ceiling, shared with every foreground
// subtask it starts, at any depth. Each turn adds its own model usage as it
// arrives, so a child's tokens are counted once, where they are spent, and
// never again when they roll up into the parent's usage.
type turnBudget struct {
	limit int64
	used  atomic.Int64
}

type turnBudgetKey struct{}

// sharedBudget is the budget a turn draws on: a new one for a root turn when
// the agent sets a limit, the starting turn's for a foreground subtask (it
// rides on the child's run context), and none otherwise.
func sharedBudget(ctx context.Context, depth, limit int) *turnBudget {
	if depth > 0 {
		b, _ := ctx.Value(turnBudgetKey{}).(*turnBudget)
		return b
	}
	if limit <= 0 {
		return nil
	}
	return &turnBudget{limit: int64(limit)}
}

// withBudget hands b to a subtask started with the returned context; a nil b
// clears any budget the context carried, as for a background subtask.
func withBudget(ctx context.Context, b *turnBudget) context.Context {
	return context.WithValue(ctx, turnBudgetKey{}, b)
}

func (b *turnBudget) add(tokens int) {
	if b != nil {
		b.used.Add(int64(tokens))
	}
}

// spent reports whether the shared budget is used up.
func (b *turnBudget) spent() bool {
	return b != nil && b.used.Load() >= b.limit
}
