package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/store"
)

// ErrDailyBudget means the agent has spent its local UTC-day token allowance.
var ErrDailyBudget = errors.New("nanite: daily token budget is spent")

const dailyBudgetKey = "daily_token_budget"

type dailyState struct {
	Day    string `json:"day"`
	Tokens int64  `json:"tokens"`
	Warned bool   `json:"warned"`
}

type dailyBudget struct {
	mu    sync.Mutex
	state dailyState
	now   func() time.Time
}

func (e *Engine) loadDaily() error {
	e.daily.now = time.Now
	raw, err := e.cfg.Store.Setting(context.Background(), "", dailyBudgetKey)
	if err != nil {
		return err
	}
	if raw != "" {
		var state dailyState
		err := json.Unmarshal([]byte(raw), &state)
		_, dayErr := time.Parse("2006-01-02", state.Day)
		if err != nil || dayErr != nil || state.Tokens < 0 {
			e.cfg.Logger.Warn("invalid daily budget state; starting an empty ledger")
			state = dailyState{Day: e.daily.now().UTC().Format("2006-01-02")}
		}
		e.daily.state = state
	}
	return nil
}

// checkDaily applies at admission and before every model call. Already-running
// requests can report usage after the threshold; this is a local admission cap.
func (e *Engine) checkDaily() error {
	e.daily.mu.Lock()
	defer e.daily.mu.Unlock()
	if e.cfg.DailyTokenBudget > 0 && e.daily.state.Day == e.daily.now().UTC().Format("2006-01-02") && e.daily.state.Tokens >= int64(e.cfg.DailyTokenBudget) {
		return fmt.Errorf("%w: %d/%d tokens on %s UTC", ErrDailyBudget, e.daily.state.Tokens, e.cfg.DailyTokenBudget, e.daily.state.Day)
	}
	return nil
}

// spendDaily counts only a call's own usage, never a child's roll-up.
func (e *Engine) spendDaily(ctx context.Context, conversation string, tokens int) error {
	if tokens <= 0 || e.cfg.Store == nil {
		return nil
	}
	e.daily.mu.Lock()
	day := e.daily.now().UTC().Format("2006-01-02")
	state := e.daily.state
	if state.Day != day {
		state = dailyState{Day: day}
	}
	if int64(tokens) > math.MaxInt64-state.Tokens {
		state.Tokens = math.MaxInt64
	} else {
		state.Tokens += int64(tokens)
	}
	if e.cfg.DailyTokenBudget == 0 {
		e.daily.state = state
		e.daily.mu.Unlock()
		return nil
	}
	warning := e.cfg.DailyTokenBudget > 0 && !state.Warned && state.Tokens >= int64(e.cfg.DailyTokenBudget)-int64(e.cfg.DailyTokenBudget)/5
	state.Warned = state.Warned || warning
	var notice *store.Notice
	body := fmt.Sprintf("Daily token budget reached 80%%: %d/%d tokens on %s UTC.", state.Tokens, e.cfg.DailyTokenBudget, day)
	if warning && conversation != "" {
		notice = &store.Notice{ID: store.NewID(time.Now()), ConversationID: conversation, Kind: "budget", Body: body}
	}
	raw, err := json.Marshal(state)
	if err == nil {
		err = e.cfg.Store.SaveBudget(context.WithoutCancel(ctx), dailyBudgetKey, string(raw), notice)
	}
	if err != nil {
		// Retain usage in memory and retry persistence on the next spend.
		// Leave the warning pending until its notice is durably committed.
		state.Warned = e.daily.state.Day == day && e.daily.state.Warned
	}
	e.daily.state = state
	e.daily.mu.Unlock()
	if err != nil {
		e.cfg.Logger.Warn("persist daily budget; retaining in-memory usage", "error", err)
		return nil
	}
	if warning {
		e.cfg.Logger.Warn(body)
		warningEvent := events.BudgetWarning{Day: day, Tokens: state.Tokens, Limit: int64(e.cfg.DailyTokenBudget)}
		e.mu.Lock()
		run := e.active[conversation]
		e.mu.Unlock()
		if run != nil {
			run.emit(ctx, func(meta events.EventMeta) events.Event { warningEvent.EventMeta = meta; return warningEvent })
		} else {
			_ = e.panics.Call("budget warning sink", func() error {
				e.runtime.Observe(ctx, conversation, "", warningEvent)
				return nil
			})
		}
		if notice != nil {
			e.announce(ctx, *notice, false)
		}
	}
	return nil
}
