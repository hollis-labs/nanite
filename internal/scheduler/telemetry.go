package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/store"
)

const CategoryScheduleFire = "schedule_fire"

type ScheduleFireOutcome string

const (
	ScheduleFireOutcomeSuccess   ScheduleFireOutcome = "success"
	ScheduleFireOutcomeRetry     ScheduleFireOutcome = "retry"
	ScheduleFireOutcomeSkipped   ScheduleFireOutcome = "skipped"
	ScheduleFireOutcomeExhausted ScheduleFireOutcome = "exhausted"
)

type TraceStore interface {
	LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string)
}

type schedulePolicyStore interface {
	GetAgentSchedule(context.Context, string) (*store.AgentSchedule, error)
}

type scheduleFireLookup interface {
	GetScheduleFire(context.Context, string) (*store.ScheduleFire, error)
}

type scheduleDisabler interface {
	DisableSchedule(context.Context, string) error
}

// PolicyObserver is the Nanite-owned boundary around go-scheduler's generic
// lifecycle. The library owns retry accounting, backoff, exhaustion and CAS;
// this observer only applies Nanite's terminal on_fail policy and emits the
// existing schedule_fire event stream after durable transitions.
type PolicyObserver struct {
	Schedules schedulePolicyStore
	Fires     scheduleFireLookup
	Disabler  scheduleDisabler
	Traces    TraceStore
	Logger    *slog.Logger
}

var _ gosched.Observer = (*PolicyObserver)(nil)

func (o *PolicyObserver) Observe(ctx context.Context, event gosched.ObserverEvent) error {
	// Workflow activations carry their own Hadron event/projection semantics.
	// They share the Fire engine, not agent_schedules on_fail policy or the
	// legacy schedule_fire telemetry category.
	if event.Fire.JobType == JobTypeWorkflowActivation {
		return nil
	}
	var outcome ScheduleFireOutcome
	switch event.Kind {
	case gosched.ObserverSuccess:
		outcome = ScheduleFireOutcomeSuccess
	case gosched.ObserverRetry:
		outcome = ScheduleFireOutcomeRetry
	case gosched.ObserverExhaustion:
		outcome = ScheduleFireOutcomeExhausted
	case gosched.ObserverSkip:
		// The engine also reports benign CAS and not-due skips. Only a
		// persisted terminal duplicate-dispatch skip is a dispatch outcome.
		if event.Fire.Status != gosched.FireSkipped {
			return nil
		}
		outcome = ScheduleFireOutcomeSkipped
	default:
		return nil
	}

	persistCtx := context.WithoutCancel(ctx)
	var joined error
	var scheduleName, onFail string
	if o.Schedules != nil && event.Fire.ScheduleID != "" {
		schedule, err := o.Schedules.GetAgentSchedule(persistCtx, event.Fire.ScheduleID)
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("observe schedule fire: read schedule: %w", err))
		} else {
			scheduleName = schedule.Name
			onFail = schedule.OnFail
		}
	}

	rowID := ""
	if o.Fires != nil && event.Fire.ID != "" {
		fire, err := o.Fires.GetScheduleFire(persistCtx, event.Fire.ID)
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("observe schedule fire: read fire: %w", err))
		} else {
			rowID = fire.ID
		}
	}

	if outcome == ScheduleFireOutcomeExhausted {
		if err := o.applyOnFail(persistCtx, event.Fire.ScheduleID, onFail, event.Err); err != nil {
			joined = errors.Join(joined, err)
		}
	}

	o.emitTrace(persistCtx, event, outcome, scheduleName, rowID, onFail)
	return joined
}

func (o *PolicyObserver) applyOnFail(ctx context.Context, scheduleID, onFail string, cause error) error {
	logger := o.logger()
	switch onFail {
	case store.ScheduleOnFailDisable:
		if o.Disabler == nil {
			return fmt.Errorf("apply schedule on_fail=disable: disabler is not configured")
		}
		if err := o.Disabler.DisableSchedule(ctx, scheduleID); err != nil {
			return fmt.Errorf("apply schedule on_fail=disable: %w", err)
		}
	case store.ScheduleOnFailNotify:
		logger.Warn("scheduled fire exhausted; operator notification required",
			"schedule_id", scheduleID, "error", cause)
	case store.ScheduleOnFailRetry, "":
		// Historical Nanite semantics stop after max_retries and leave the
		// recurring schedule active so its next occurrence can run normally.
		logger.Warn("scheduled fire exhausted; leaving schedule active",
			"schedule_id", scheduleID, "error", cause)
	default:
		return fmt.Errorf("apply schedule on_fail: unknown policy %q", onFail)
	}
	return nil
}

type traceRecord struct {
	ScheduleID       string          `json:"schedule_id"`
	ScheduleName     string          `json:"schedule_name,omitempty"`
	RunID            string          `json:"run_id"`
	ScheduleRunRowID string          `json:"schedule_run_row_id,omitempty"`
	JobType          string          `json:"job_type"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	Outcome          string          `json:"outcome"`
	AttemptCount     int64           `json:"attempt_count"`
	MaxRetries       int64           `json:"max_retries,omitempty"`
	OnFail           string          `json:"on_fail,omitempty"`
	Error            string          `json:"error,omitempty"`
	Reason           string          `json:"reason,omitempty"`
}

func (o *PolicyObserver) emitTrace(
	ctx context.Context,
	event gosched.ObserverEvent,
	outcome ScheduleFireOutcome,
	scheduleName, rowID, onFail string,
) {
	if o.Traces == nil {
		return
	}
	record := traceRecord{
		ScheduleID:       event.Fire.ScheduleID,
		ScheduleName:     scheduleName,
		RunID:            event.Fire.ID,
		ScheduleRunRowID: rowID,
		JobType:          event.Fire.JobType,
		Outcome:          string(outcome),
		AttemptCount:     int64(event.Fire.Attempt),
		Reason:           event.Reason,
	}
	if outcome == ScheduleFireOutcomeRetry || outcome == ScheduleFireOutcomeExhausted {
		record.MaxRetries = int64(event.Fire.Retry.MaxAttempts)
		record.OnFail = onFail
	}
	if len(event.Fire.Payload) > 0 && json.Valid(event.Fire.Payload) {
		record.Payload = append(json.RawMessage(nil), event.Fire.Payload...)
	}
	if event.Err != nil {
		record.Error = event.Err.Error()
	}
	metadata, err := json.Marshal(record)
	if err != nil {
		o.logger().Warn("scheduler: marshal schedule-fire trace", "error", err)
		metadata = []byte("{}")
	}
	detail := scheduleName
	if detail == "" {
		detail = event.Fire.ScheduleID
	}
	o.Traces.LogEvent(ctx, "", event.Fire.JobType, CategoryScheduleFire, detail, string(metadata))
}

func (o *PolicyObserver) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}
