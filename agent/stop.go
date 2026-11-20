package agent

import (
	"context"

	"github.com/hollis-labs/nanite/internal/engine"
)

// ErrStopped is returned when work is submitted after StopAll.
var ErrStopped = engine.ErrStopped

// ErrDailyBudget is returned when the agent's UTC-day token budget is spent.
var ErrDailyBudget = engine.ErrDailyBudget

// StopAll cancels turns, subtasks and background jobs, waits for their cleanup,
// and prevents new work. Close and reopen the agent to accept work again.
// If ctx ends, cleanup continues; the agent remains stopped.
func (a *Agent) StopAll(ctx context.Context) error { return a.engine.StopAll(ctx) }
