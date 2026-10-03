package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"
	"github.com/hollis-labs/nanite/internal/store"
)

// SQLStoreAdapter keeps host due-time wake resolution while delegating neutral
// lifecycle and receipts to the released SQLStore facade. It is constructed
// explicitly and does not select or change storage authority. Engine wiring
// must follow a qualified offline cutover and authority preflight.
type SQLStoreAdapter struct {
	*store.SchedulerSQLStore
	legacy *StoreAdapter
}

var _ gosched.Store = (*SQLStoreAdapter)(nil)
var _ DispatchReceiptStore = (*SQLStoreAdapter)(nil)

func NewSQLStoreAdapter(host *store.Store, logger *slog.Logger) (*SQLStoreAdapter, error) {
	if logger == nil {
		return nil, fmt.Errorf("scheduler sqlstore: logger is required")
	}
	shared, err := store.NewSchedulerSQLStore(host)
	if err != nil {
		return nil, err
	}
	return &SQLStoreAdapter{SchedulerSQLStore: shared, legacy: &StoreAdapter{Store: host, Logger: logger}}, nil
}

func (a *SQLStoreAdapter) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]gosched.Schedule, error) {
	rows, err := a.SchedulerSQLStore.ListDueSchedules(ctx, now, -1)
	if err != nil {
		return nil, err
	}
	out := make([]gosched.Schedule, 0, len(rows))
	for _, sch := range rows {
		family, err := a.ScheduleFamily(ctx, sch.ID)
		if err != nil {
			return nil, err
		}
		if family == "agent" {
			row, err := a.GetAgentSchedule(ctx, sch.ID)
			if errors.Is(err, store.ErrAgentScheduleNotFound) {
				a.legacy.logConversionSkip(sch.ID, "", sch.JobType, err)
				continue
			}
			if err != nil {
				return nil, err
			}
			payload, err := a.legacy.buildPayload(*row)
			if err != nil {
				a.legacy.logConversionSkip(row.ID, row.AgentID, row.JobType, err)
				continue
			}
			sch.Payload = payload
		}
		out = append(out, sch)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}
